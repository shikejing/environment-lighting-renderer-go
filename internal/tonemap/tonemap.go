// Package tonemap converts HDR linear radiance to display-referred 8-bit.
//
// Extreme-value policy (deterministic, documented):
//   - NaN components are replaced by 0.
//   - +Inf components are replaced by MaxLuminance; -Inf by 0.
//   - Negative components are clamped to 0.
//   - After tone mapping, values are clamped to [0,1] before gamma.
package tonemap

import (
	"image/color"
	"math"

	"github.com/shikejing/environment-lighting-renderer-go/internal/geom"
)

// Operator selects the tone-mapping curve.
type Operator int

const (
	// OpClamp applies exposure then hard-clamps to [0,1].
	OpClamp Operator = iota
	// OpReinhard applies the classic Reinhard curve x/(1+x) per channel.
	OpReinhard
	// OpACES applies the ACES filmic approximation (Narkowicz).
	OpACES
)

func (o Operator) String() string {
	switch o {
	case OpClamp:
		return "clamp"
	case OpReinhard:
		return "reinhard"
	case OpACES:
		return "aces"
	}
	return "unknown"
}

// ParseOperator converts a name to an Operator.
func ParseOperator(s string) (Operator, bool) {
	switch s {
	case "clamp":
		return OpClamp, true
	case "reinhard":
		return OpReinhard, true
	case "aces":
		return OpACES, true
	}
	return 0, false
}

// MaxLuminance bounds finite replacements for +Inf and the upper clamp
// before tone mapping. Deterministic for any finite or infinite input.
const MaxLuminance = 1e30

// Config holds tone-mapping parameters.
type Config struct {
	Exposure float64  // linear multiplier applied before the curve; default 1
	Gamma    float64  // display gamma; default 2.2
	Op       Operator // tone-mapping curve
}

func (c Config) withDefaults() Config {
	if c.Exposure == 0 {
		c.Exposure = 1
	}
	if c.Gamma == 0 {
		c.Gamma = 2.2
	}
	return c
}

// sanitize applies the documented extreme-value policy.
func sanitize(v float64) float64 {
	switch {
	case math.IsNaN(v):
		return 0
	case math.IsInf(v, 1):
		return MaxLuminance
	case math.IsInf(v, -1):
		return 0
	case v < 0:
		return 0
	case v > MaxLuminance:
		return MaxLuminance
	}
	return v
}

func curve(op Operator, x float64) float64 {
	switch op {
	case OpReinhard:
		return x / (1 + x)
	case OpACES:
		// Narkowicz ACES approximation.
		const a, b, c, d, e = 2.51, 0.03, 2.43, 0.59, 0.14
		return (x * (a*x + b)) / (x*(c*x+d) + e)
	default: // OpClamp
		return x
	}
}

// Map converts one HDR pixel to a display value in [0,1]^3.
func Map(c geom.Vec3, cfg Config) geom.Vec3 {
	cfg = cfg.withDefaults()
	f := func(v float64) float64 {
		v = sanitize(v) * cfg.Exposure
		v = sanitize(v) // exposure may overflow to +Inf
		v = curve(cfg.Op, v)
		if v < 0 {
			v = 0
		}
		if v > 1 {
			v = 1
		}
		v = math.Pow(v, 1/cfg.Gamma)
		return v
	}
	return geom.Vec3{X: f(c.X), Y: f(c.Y), Z: f(c.Z)}
}

// ToRGBA converts an HDR pixel to 8-bit RGBA (alpha 255).
func ToRGBA(c geom.Vec3, cfg Config) color.RGBA {
	m := Map(c, cfg)
	to8 := func(v float64) uint8 { return uint8(v*255 + 0.5) }
	return color.RGBA{R: to8(m.X), G: to8(m.Y), B: to8(m.Z), A: 255}
}
