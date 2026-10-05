package scene

import (
	"github.com/shikejing/environment-lighting-renderer-go/geom"
)

// BVH is a bounding volume hierarchy over primitives.
//
// It supports incremental Insert/Remove/Replace without full rebuilds:
//   - Insert descends to the cheapest leaf (surface-area heuristic) and
//     grafts a new leaf, refitting ancestor boxes.
//   - Remove detaches the leaf and collapses its parent, refitting upward.
//
// Query results are topology-independent: closest-hit ties are broken by
// primitive id, so an incrementally updated tree returns exactly the same
// hits as a fresh full rebuild over the same primitive set.
//
// A BVH must not be used concurrently while being mutated;
// synchronization is the caller's job (see Scene).
type BVH struct {
	root   *bvhNode
	nextID int
	index  map[int]*bvhNode // prim id -> leaf containing it
}

type bvhNode struct {
	box    geom.AABB
	parent *bvhNode
	left   *bvhNode
	right  *bvhNode
	prims  []Primitive // non-nil on leaves
}

func (n *bvhNode) isLeaf() bool { return n.prims != nil }

const leafMaxPrims = 4
const sahBins = 12

// NewBVH builds an empty BVH.
func NewBVH() *BVH { return &BVH{index: make(map[int]*bvhNode)} }

// Rebuild replaces the whole content with a fresh build over prims.
// NOTE: Rebuild assigns ids to the given primitives, so prims must
// not be shared with another live BVH — clone them first (ClonePrims).
func (b *BVH) Rebuild(prims []Primitive) {
	b.root = nil
	b.nextID = 0
	b.index = make(map[int]*bvhNode)
	if len(prims) == 0 {
		return
	}
	cp := make([]Primitive, len(prims))
	copy(cp, prims)
	b.root = b.build(cp)
	b.indexLeaves(b.root)
}

func (b *BVH) indexLeaves(n *bvhNode) {
	if n.isLeaf() {
		for _, p := range n.prims {
			b.index[p.getID()] = n
		}
		return
	}
	b.indexLeaves(n.left)
	b.indexLeaves(n.right)
}

// build constructs a subtree via binned-SAH top-down splitting and
// assigns fresh ids in a deterministic (input) order.
func (b *BVH) build(prims []Primitive) *bvhNode {
	box := geom.EmptyAABB()
	for _, p := range prims {
		p.setID(b.nextID)
		b.nextID++
		box = box.Union(p.Bounds())
	}
	n := &bvhNode{box: box}
	if len(prims) <= leafMaxPrims {
		n.prims = append([]Primitive(nil), prims...)
		return n
	}
	// Centroid bounds to choose split axis.
	cb := geom.EmptyAABB()
	for _, p := range prims {
		cb = cb.Expand(p.Bounds().Centroid())
	}
	ext := cb.Max.Sub(cb.Min)
	axis := 0
	if ext.Y > ext.X && ext.Y >= ext.Z {
		axis = 1
	} else if ext.Z > ext.X && ext.Z > ext.Y {
		axis = 2
	}
	c0 := axisVal(cb.Min, axis)
	span := axisVal(cb.Max, axis) - c0
	if span <= 0 {
		// All centroids coincide: split in half deterministically.
		half := len(prims) / 2
		n.left = b.build(prims[:half])
		n.right = b.build(prims[half:])
	} else {
		type bin struct {
			box   geom.AABB
			count int
		}
		var bins [sahBins]bin
		for i := range bins {
			bins[i].box = geom.EmptyAABB()
		}
		for _, p := range prims {
			c := axisVal(p.Bounds().Centroid(), axis)
			i := int(float64(sahBins) * (c - c0) / span)
			if i >= sahBins {
				i = sahBins - 1
			}
			bins[i].box = bins[i].box.Union(p.Bounds())
			bins[i].count++
		}
		// Sweep for the cheapest split.
		bestCost := geom.V(0, 0, 0).X + 1e30
		bestBin := -1
		leftBox := geom.EmptyAABB()
		leftCount := 0
		rightBox := make([]geom.AABB, sahBins)
		rightCount := make([]int, sahBins)
		rb := geom.EmptyAABB()
		rc := 0
		for i := sahBins - 1; i >= 0; i-- {
			rb = rb.Union(bins[i].box)
			rc += bins[i].count
			rightBox[i], rightCount[i] = rb, rc
		}
		for i := 0; i < sahBins-1; i++ {
			leftBox = leftBox.Union(bins[i].box)
			leftCount += bins[i].count
			if leftCount == 0 || rightCount[i+1] == 0 {
				continue
			}
			cost := leftBox.SurfaceArea()*float64(leftCount) +
				rightBox[i+1].SurfaceArea()*float64(rightCount[i+1])
			if cost < bestCost {
				bestCost = cost
				bestBin = i
			}
		}
		if bestBin < 0 {
			half := len(prims) / 2
			n.left = b.build(prims[:half])
			n.right = b.build(prims[half:])
		} else {
			var lp, rp []Primitive
			for _, p := range prims {
				c := axisVal(p.Bounds().Centroid(), axis)
				i := int(float64(sahBins) * (c - c0) / span)
				if i >= sahBins {
					i = sahBins - 1
				}
				if i <= bestBin {
					lp = append(lp, p)
				} else {
					rp = append(rp, p)
				}
			}
			n.left = b.build(lp)
			n.right = b.build(rp)
		}
	}
	n.left.parent = n
	n.right.parent = n
	return n
}

func axisVal(v geom.Vec3, axis int) float64 {
	switch axis {
	case 0:
		return v.X
	case 1:
		return v.Y
	default:
		return v.Z
	}
}

// Insert adds a primitive incrementally and returns its id.
func (b *BVH) Insert(p Primitive) int {
	id := b.nextID
	b.nextID++
	p.setID(id)
	leaf := &bvhNode{box: p.Bounds(), prims: []Primitive{p}}
	b.index[id] = leaf
	if b.root == nil {
		b.root = leaf
		return id
	}
	// Descend to the leaf whose expansion is cheapest (SAH cost).
	node := b.root
	for !node.isLeaf() {
		node.box = node.box.Union(leaf.box)
		costL := node.left.box.Union(leaf.box).SurfaceArea() - node.left.box.SurfaceArea()
		costR := node.right.box.Union(leaf.box).SurfaceArea() - node.right.box.SurfaceArea()
		if costL < costR {
			node = node.left
		} else {
			node = node.right
		}
	}
	// Graft: replace `node` with a new internal node joining node+leaf.
	parent := &bvhNode{
		box:   node.box.Union(leaf.box),
		left:  node,
		right: leaf,
	}
	b.reattach(node, parent) // must run before node.parent is overwritten
	node.parent, leaf.parent = parent, parent
	b.refitUp(parent.parent)
	return id
}

// reattach swaps child `old` for `now` in old's parent (or root).
func (b *BVH) reattach(old, now *bvhNode) {
	now.parent = old.parent
	if old.parent == nil {
		b.root = now
		return
	}
	if old.parent.left == old {
		old.parent.left = now
	} else {
		old.parent.right = now
	}
}

// refitUp recomputes ancestor boxes from n upward until stable.
func (b *BVH) refitUp(n *bvhNode) {
	for n != nil {
		nb := n.left.box.Union(n.right.box)
		if nb == n.box {
			return
		}
		n.box = nb
		n = n.parent
	}
}

// Remove deletes the primitive with the given id.
func (b *BVH) Remove(id int) bool {
	leaf, ok := b.index[id]
	if !ok {
		return false
	}
	delete(b.index, id)
	for i, p := range leaf.prims {
		if p.getID() == id {
			leaf.prims = append(leaf.prims[:i], leaf.prims[i+1:]...)
			break
		}
	}
	if len(leaf.prims) > 0 {
		// Leaf still holds other prims: just shrink its box.
		leaf.box = geom.EmptyAABB()
		for _, p := range leaf.prims {
			leaf.box = leaf.box.Union(p.Bounds())
		}
		b.refitUp(leaf.parent)
		return true
	}
	// Leaf is empty: collapse its parent into the sibling.
	parent := leaf.parent
	if parent == nil {
		b.root = nil
		return true
	}
	sibling := parent.left
	if sibling == leaf {
		sibling = parent.right
	}
	// Leaves themselves do not move, so b.index stays valid.
	b.reattach(parent, sibling)
	b.refitUp(sibling.parent)
	return true
}

// Replace removes id and inserts p, returning the new id.
func (b *BVH) Replace(id int, p Primitive) (int, bool) {
	if !b.Remove(id) {
		return -1, false
	}
	return b.Insert(p), true
}

// Intersect finds the closest hit along the ray. Ties at exactly equal
// distance are broken by primitive id so the result does not depend on
// tree topology (incremental update == full rebuild).
func (b *BVH) Intersect(r geom.Ray, tMin, tMax float64) (Hit, bool) {
	if b.root == nil {
		return Hit{}, false
	}
	best := Hit{ID: -1}
	found := false
	bestT := tMax
	stack := [64]*bvhNode{}
	sp := 0
	stack[sp] = b.root
	sp++
	for sp > 0 {
		sp--
		n := stack[sp]
		if !n.box.Intersect(r, tMin, bestT) {
			continue
		}
		if n.isLeaf() {
			for _, p := range n.prims {
				h, ok := p.Intersect(r, tMin, bestT)
				if !ok {
					continue
				}
				if !found || h.T < bestT || (h.T == bestT && h.ID < best.ID) {
					best, bestT, found = h, h.T, true
				}
			}
			continue
		}
		if sp+2 > len(stack) {
			// Stack overflow guard: fall back to recursion (deeply
			// unbalanced trees after many incremental inserts).
			if h, ok := intersectRec(n.left, r, tMin, bestT, best, found); ok {
				best, bestT, found = h, h.T, true
			}
			if h, ok := intersectRec(n.right, r, tMin, bestT, best, found); ok {
				best, bestT, found = h, h.T, true
			}
			continue
		}
		stack[sp] = n.left
		sp++
		stack[sp] = n.right
		sp++
	}
	return best, found
}

func intersectRec(n *bvhNode, r geom.Ray, tMin, tMax float64, best Hit, found bool) (Hit, bool) {
	if !n.box.Intersect(r, tMin, tMax) {
		return best, found
	}
	if n.isLeaf() {
		for _, p := range n.prims {
			h, ok := p.Intersect(r, tMin, tMax)
			if !ok {
				continue
			}
			if !found || h.T < tMax || (h.T == tMax && h.ID < best.ID) {
				best, tMax, found = h, h.T, true
			}
		}
		return best, found
	}
	best, found = intersectRec(n.left, r, tMin, tMax, best, found)
	if found {
		tMax = best.T
	}
	return intersectRec(n.right, r, tMin, tMax, best, found)
}

// Occluded reports whether anything blocks the ray within (tMin, tMax).
func (b *BVH) Occluded(r geom.Ray, tMin, tMax float64) bool {
	if b.root == nil {
		return false
	}
	stack := [64]*bvhNode{}
	sp := 0
	stack[sp] = b.root
	sp++
	for sp > 0 {
		sp--
		n := stack[sp]
		if !n.box.Intersect(r, tMin, tMax) {
			continue
		}
		if n.isLeaf() {
			for _, p := range n.prims {
				if _, ok := p.Intersect(r, tMin, tMax); ok {
					return true
				}
			}
			continue
		}
		if sp+2 > len(stack) {
			if occludedRec(n.left, r, tMin, tMax) || occludedRec(n.right, r, tMin, tMax) {
				return true
			}
			continue
		}
		stack[sp] = n.left
		sp++
		stack[sp] = n.right
		sp++
	}
	return false
}

func occludedRec(n *bvhNode, r geom.Ray, tMin, tMax float64) bool {
	if !n.box.Intersect(r, tMin, tMax) {
		return false
	}
	if n.isLeaf() {
		for _, p := range n.prims {
			if _, ok := p.Intersect(r, tMin, tMax); ok {
				return true
			}
		}
		return false
	}
	return occludedRec(n.left, r, tMin, tMax) || occludedRec(n.right, r, tMin, tMax)
}

// ClonePrims returns independent copies of the given primitives,
// suitable for handing to another BVH (ids are not copied).
func ClonePrims(prims []Primitive) []Primitive {
	out := make([]Primitive, 0, len(prims))
	for _, p := range prims {
		switch v := p.(type) {
		case *Sphere:
			cp := *v
			cp.id = -1
			out = append(out, &cp)
		case *Triangle:
			cp := *v
			cp.id = -1
			out = append(out, &cp)
		}
	}
	return out
}

// AlbedoOf returns the albedo of the primitive with the given id
// (zero vector if the id is not live).
func (b *BVH) AlbedoOf(id int) geom.Vec3 {
	leaf, ok := b.index[id]
	if !ok {
		return geom.Vec3{}
	}
	for _, p := range leaf.prims {
		if p.getID() == id {
			return p.Albedo()
		}
	}
	return geom.Vec3{}
}

// Prims returns a snapshot of live primitives (for rebuild/compare in tests).
func (b *BVH) Prims() []Primitive {
	if b.root == nil {
		return nil
	}
	var out []Primitive
	var walk func(n *bvhNode)
	walk = func(n *bvhNode) {
		if n.isLeaf() {
			out = append(out, n.prims...)
			return
		}
		walk(n.left)
		walk(n.right)
	}
	walk(b.root)
	return out
}
