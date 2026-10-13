package sim

import "math"

// Vec is a 2D vector on the pitch plane (X = long axis, Z = short axis).
type Vec struct {
	X float64 `json:"x"`
	Z float64 `json:"z"`
}

func (a Vec) Add(b Vec) Vec       { return Vec{a.X + b.X, a.Z + b.Z} }
func (a Vec) Sub(b Vec) Vec       { return Vec{a.X - b.X, a.Z - b.Z} }
func (a Vec) Scale(s float64) Vec { return Vec{a.X * s, a.Z * s} }
func (a Vec) Dot(b Vec) float64   { return a.X*b.X + a.Z*b.Z }
func (a Vec) Len() float64        { return math.Hypot(a.X, a.Z) }

// Norm returns the unit vector, or the zero vector when a is (almost) zero.
func (a Vec) Norm() Vec {
	l := a.Len()
	if l < 1e-9 {
		return Vec{}
	}
	return Vec{a.X / l, a.Z / l}
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }
