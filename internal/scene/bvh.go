package scene

import (
	"sort"

	"github.com/shikejing/environment-lighting-renderer-go/internal/geom"
)

// bvh is an immutable bounding-volume hierarchy. Mutations copy only the
// nodes along the affected root-to-leaf path (copy-on-write) and return a
// new root, so a previously published root stays valid for concurrent
// readers (snapshot isolation during incremental updates).
type bvhNode struct {
	box         geom.AABB
	left, right *bvhNode // nil for leaves
	id          ID       // valid only for leaves
	leaf        bool
	leaves      int // number of leaves in subtree
	built       int // leaf count when this subtree was last (re)built
}

func leafNode(id ID, box geom.AABB) *bvhNode {
	return &bvhNode{box: box, id: id, leaf: true, leaves: 1, built: 1}
}

func internalNode(l, r *bvhNode) *bvhNode {
	return &bvhNode{box: l.box.Union(r.box), left: l, right: r, leaves: l.leaves + r.leaves}
}

// buildBVH constructs a balanced BVH over the given items (full build).
// Deterministic: items are sorted by (axis coordinate, id) at each split.
func buildBVH(ids []ID, boxes map[ID]geom.AABB) *bvhNode {
	if len(ids) == 0 {
		return nil
	}
	items := make([]ID, len(ids))
	copy(items, ids)
	n := buildRecursive(items, boxes)
	n.built = n.leaves
	markBuilt(n)
	return n
}

func markBuilt(n *bvhNode) {
	if n == nil {
		return
	}
	n.built = n.leaves
	markBuilt(n.left)
	markBuilt(n.right)
}

func buildRecursive(ids []ID, boxes map[ID]geom.AABB) *bvhNode {
	if len(ids) == 1 {
		return leafNode(ids[0], boxes[ids[0]])
	}
	// Compute centroid bounds to pick split axis.
	cb := geom.EmptyAABB()
	for _, id := range ids {
		b := boxes[id]
		c := geom.Scale(geom.Add(b.Min, b.Max), 0.5)
		cb = cb.UnionPoint(c)
	}
	ext := geom.Sub(cb.Max, cb.Min)
	axis := 0
	if ext.Y > ext.X && ext.Y >= ext.Z {
		axis = 1
	} else if ext.Z > ext.X && ext.Z > ext.Y {
		axis = 2
	}
	key := func(id ID) float64 {
		b := boxes[id]
		c := geom.Scale(geom.Add(b.Min, b.Max), 0.5)
		switch axis {
		case 0:
			return c.X
		case 1:
			return c.Y
		default:
			return c.Z
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := key(ids[i]), key(ids[j])
		if a != b {
			return a < b
		}
		return ids[i] < ids[j]
	})
	mid := len(ids) / 2
	return internalNode(buildRecursive(ids[:mid], boxes), buildRecursive(ids[mid:], boxes))
}

// insert returns a new tree with the given leaf added, copying only the
// insertion path. Subtrees whose leaf count has more than doubled since
// they were built are rebuilt locally (never the whole tree).
func (n *bvhNode) insert(id ID, box geom.AABB) *bvhNode {
	if n == nil {
		return leafNode(id, box)
	}
	if n.leaf {
		return internalNode(n, leafNode(id, box))
	}
	// Local rebuild heuristic: subtree grew beyond 2x its built size.
	if n.leaves+1 > 2*n.built && n.built >= 8 {
		ids := make([]ID, 0, n.leaves+1)
		n.collect(&ids)
		ids = append(ids, id)
		boxes := make(map[ID]geom.AABB, len(ids))
		n.collectBoxes(boxes)
		boxes[id] = box
		return buildBVH(ids, boxes)
	}
	// Descend toward the child whose bounds would grow less.
	growL := unionArea(n.left.box, box) - area(n.left.box)
	growR := unionArea(n.right.box, box) - area(n.right.box)
	if growL < growR || (growL == growR && n.left.leaves <= n.right.leaves) {
		return internalNode(n.left.insert(id, box), n.right)
	}
	return internalNode(n.left, n.right.insert(id, box))
}

// remove returns a new tree without the leaf for id. found reports whether
// the leaf existed.
func (n *bvhNode) remove(id ID) (*bvhNode, bool) {
	if n == nil {
		return nil, false
	}
	if n.leaf {
		if n.id == id {
			return nil, true
		}
		return n, false
	}
	if nl, ok := n.left.remove(id); ok {
		if nl == nil {
			return n.right, true // collapse parent
		}
		return internalNode(nl, n.right), true
	}
	if nr, ok := n.right.remove(id); ok {
		if nr == nil {
			return n.left, true
		}
		return internalNode(n.left, nr), true
	}
	return n, false
}

func (n *bvhNode) collect(ids *[]ID) {
	if n.leaf {
		*ids = append(*ids, n.id)
		return
	}
	n.left.collect(ids)
	n.right.collect(ids)
}

func (n *bvhNode) collectBoxes(m map[ID]geom.AABB) {
	if n.leaf {
		m[n.id] = n.box
		return
	}
	n.left.collectBoxes(m)
	n.right.collectBoxes(m)
}

func area(b geom.AABB) float64 {
	d := geom.Sub(b.Max, b.Min)
	if d.X < 0 || d.Y < 0 || d.Z < 0 {
		return 0
	}
	return 2 * (d.X*d.Y + d.Y*d.Z + d.Z*d.X)
}

func unionArea(a, b geom.AABB) float64 { return area(a.Union(b)) }

// HitRecord describes a ray-primitive intersection.
type HitRecord struct {
	T      float64
	ID     ID
	Normal geom.Vec3
}

// intersectBVH finds the nearest hit in (tmin, tmax). Traversal order is
// deterministic so results are reproducible bit-for-bit.
func (n *bvhNode) intersect(r geom.Ray, tmin, tmax float64, spheres map[ID]Sphere) (HitRecord, bool) {
	var best HitRecord
	hit := false
	stack := [64]*bvhNode{}
	sp := 0
	push := func(m *bvhNode) {
		if sp < len(stack) {
			stack[sp] = m
			sp++
		}
	}
	cur := n
	for {
		for cur != nil && !cur.leaf {
			if !cur.box.Hit(r, tmin, tmax) {
				break
			}
			// Visit nearer child first for earlier tmax tightening.
			l, rr := cur.left, cur.right
			if !firstNear(r, l, rr) {
				l, rr = rr, l
			}
			push(rr)
			cur = l
			continue
		}
		if cur != nil && cur.leaf {
			if cur.box.Hit(r, tmin, tmax) {
				s := spheres[cur.id]
				if t, ok := s.intersect(r, tmin, tmax); ok {
					hit = true
					best.T = t
					best.ID = cur.id
					best.Normal = geom.Norm(geom.Sub(r.At(t), s.Center))
					tmax = t
				}
			}
		}
		if sp == 0 {
			break
		}
		sp--
		cur = stack[sp]
	}
	return best, hit
}

// occluded reports whether anything is hit in (tmin, tmax); early-exits.
func (n *bvhNode) occluded(r geom.Ray, tmin, tmax float64, spheres map[ID]Sphere) bool {
	if n == nil || !n.box.Hit(r, tmin, tmax) {
		return false
	}
	if n.leaf {
		s := spheres[n.id]
		_, ok := s.intersect(r, tmin, tmax)
		return ok
	}
	return n.left.occluded(r, tmin, tmax, spheres) || n.right.occluded(r, tmin, tmax, spheres)
}

func firstNear(r geom.Ray, a, b *bvhNode) bool {
	ca := geom.Scale(geom.Add(a.box.Min, a.box.Max), 0.5)
	cb := geom.Scale(geom.Add(b.box.Min, b.box.Max), 0.5)
	da := geom.Dot(geom.Sub(ca, r.Orig), r.Dir)
	db := geom.Dot(geom.Sub(cb, r.Orig), r.Dir)
	return da <= db
}
