package sim

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	MaxSkillSlots   = 3
	MaxEffects      = 4
	MaxPower        = 50.0
	MaxVisualChars  = 6000
	MaxVisualMillis = 3000
)

// Effect is one gameplay primitive of the skill DSL.
type Effect struct {
	Type      string   `json:"type"`
	Power     float64  `json:"power,omitempty"`
	Radius    float64  `json:"radius,omitempty"`
	Speed     float64  `json:"speed,omitempty"`
	Lifetime  float64  `json:"lifetime,omitempty"`
	Duration  float64  `json:"duration,omitempty"`
	Strength  float64  `json:"strength,omitempty"`
	Length    float64  `json:"length,omitempty"`
	Distance  float64  `json:"distance,omitempty"`
	Lift      float64  `json:"lift,omitempty"`      // vertical kick of the ball: it flies over players
	Direction string   `json:"direction,omitempty"` // aim | toward | away
	Targets   []string `json:"targets,omitempty"`   // ball | enemies | allies
	Status    string   `json:"status,omitempty"`    // freeze | slow | haste | shield
	Target    string   `json:"target,omitempty"`    // self | enemies
	OnHit     []Effect `json:"on_hit,omitempty"`
}

// Visual is client-side only; the server merely stores and screens it.
type Visual struct {
	Code       string `json:"code"`
	DurationMs int    `json:"duration_ms"`
	Color      string `json:"color,omitempty"`
}

type SkillSpec struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Effects     []Effect `json:"effects"`
	Visual      Visual   `json:"visual"`
}

// Skill is a validated spec plus the economy numbers derived from it.
type Skill struct {
	ID         string    `json:"id"`
	Owner      string    `json:"owner"`
	Slot       int       `json:"slot"`
	Spec       SkillSpec `json:"spec"`
	Power      float64   `json:"power"`
	CreateCost float64   `json:"create_cost"`
	CastCost   float64   `json:"cast_cost"`
	Cooldown   float64   `json:"cooldown"`
	Recoil     float64   `json:"recoil"`
	Reach      float64   `json:"reach"`
}

var bannedVisual = regexp.MustCompile(`(?i)\b(window|document|globalThis|self|top|parent|frames|fetch|XMLHttpRequest|WebSocket|EventSource|import|require|eval|Function|constructor|prototype|__proto__|__defineGetter__|localStorage|sessionStorage|indexedDB|setTimeout|setInterval|requestAnimationFrame|postMessage|Worker|SharedWorker|navigator|location|cookie|Reflect|Proxy|Atomics|WebAssembly|process)\b`)

// ValidateVisual rejects visual code that is too big or touches banned globals.
func ValidateVisual(v Visual) error {
	if strings.TrimSpace(v.Code) == "" {
		return fmt.Errorf("visual.code is required (Three.js function body, see skill_guide)")
	}
	if len(v.Code) > MaxVisualChars {
		return fmt.Errorf("visual.code too long: %d > %d chars", len(v.Code), MaxVisualChars)
	}
	if v.DurationMs < 200 || v.DurationMs > MaxVisualMillis {
		return fmt.Errorf("visual.duration_ms must be 200..%d", MaxVisualMillis)
	}
	if !strings.Contains(v.Code, "ctx.group.add") {
		return fmt.Errorf("visual.code adds nothing to the scene: call ctx.group.add(...)")
	}
	if m := bannedVisual.FindString(v.Code); m != "" {
		return fmt.Errorf("visual.code uses forbidden identifier %q", m)
	}
	if strings.Contains(v.Code, "[\"") || strings.Contains(v.Code, "['") || strings.Contains(v.Code, "`") {
		// computed/string property access is how sandboxes get escaped; allow only dot access.
		return fmt.Errorf("visual.code must not use bracket-string property access or template literals")
	}
	return nil
}

func inRange(name string, v, lo, hi float64) error {
	if v < lo || v > hi {
		return fmt.Errorf("%s=%v out of range [%v, %v]", name, v, lo, hi)
	}
	return nil
}

func oneOf(name, v string, allowed ...string) error {
	for _, a := range allowed {
		if v == a {
			return nil
		}
	}
	return fmt.Errorf("%s=%q must be one of %v", name, v, allowed)
}

// scoreEffect validates one effect and returns its power score and reach (world units).
func scoreEffect(e *Effect, nested bool) (power, reach float64, err error) {
	if e.Direction == "" {
		e.Direction = "aim"
	}
	switch e.Type {
	case "dash_self":
		if err = inRange("dash_self.power", e.Power, 1, 30); err != nil {
			return
		}
		if err = oneOf("dash_self.direction", e.Direction, "aim", "away"); err != nil {
			return
		}
		return e.Power * 0.3, e.Power * 0.4, nil
	case "impulse_ball":
		if err = inRange("impulse_ball.power", e.Power, 1, 45); err != nil {
			return
		}
		if err = inRange("impulse_ball.radius", e.Radius, 1, 10); err != nil {
			return
		}
		if err = oneOf("impulse_ball.direction", e.Direction, "aim", "toward", "away"); err != nil {
			return
		}
		if err = inRange("impulse_ball.lift", e.Lift, 0, 30); err != nil {
			return
		}
		return e.Power*0.4 + e.Radius*0.5 + e.Lift*0.25, e.Radius, nil
	case "lob_ball":
		if nested {
			return 0, 0, fmt.Errorf("lob_ball cannot be nested in on_hit")
		}
		if err = inRange("lob_ball.power", e.Power, 3, 40); err != nil {
			return
		}
		if err = inRange("lob_ball.lift", e.Lift, 8, 30); err != nil {
			return
		}
		if err = inRange("lob_ball.radius", e.Radius, 1, 10); err != nil {
			return
		}
		return e.Power*0.35 + e.Lift*0.3 + e.Radius*0.5, e.Radius, nil
	case "blast":
		if err = inRange("blast.radius", e.Radius, 2, 20); err != nil {
			return
		}
		if err = inRange("blast.power", e.Power, 1, 40); err != nil {
			return
		}
		if len(e.Targets) == 0 {
			e.Targets = []string{"ball"}
		}
		for _, t := range e.Targets {
			if err = oneOf("blast.targets", t, "ball", "enemies", "allies"); err != nil {
				return
			}
		}
		if err = inRange("blast.lift", e.Lift, 0, 25); err != nil {
			return
		}
		return e.Radius*0.5 + e.Power*0.4 + 3*float64(len(e.Targets)-1) + e.Lift*0.2, e.Radius, nil
	case "projectile":
		if nested {
			return 0, 0, fmt.Errorf("projectile cannot be nested in on_hit")
		}
		if err = inRange("projectile.speed", e.Speed, 5, 60); err != nil {
			return
		}
		if err = inRange("projectile.radius", e.Radius, 0.5, 3); err != nil {
			return
		}
		if err = inRange("projectile.lifetime", e.Lifetime, 0.2, 3); err != nil {
			return
		}
		if len(e.OnHit) == 0 || len(e.OnHit) > 2 {
			return 0, 0, fmt.Errorf("projectile.on_hit needs 1..2 effects (push via impulse_ball/blast, or status)")
		}
		p := e.Speed*0.08 + e.Radius*2 + e.Lifetime*1.5
		for i := range e.OnHit {
			if e.OnHit[i].Type != "blast" && e.OnHit[i].Type != "status" {
				return 0, 0, fmt.Errorf("on_hit effects must be blast or status")
			}
			hp, _, herr := scoreEffect(&e.OnHit[i], true)
			if herr != nil {
				return 0, 0, herr
			}
			p += hp
		}
		return p, e.Speed * e.Lifetime, nil
	case "status":
		if err = oneOf("status.status", e.Status, "freeze", "slow", "haste", "shield"); err != nil {
			return
		}
		if err = oneOf("status.target", e.Target, "self", "enemies"); err != nil {
			return
		}
		if err = inRange("status.duration", e.Duration, 0.5, 5); err != nil {
			return
		}
		if e.Target == "enemies" {
			if err = inRange("status.radius", e.Radius, 2, 15); err != nil {
				return
			}
		}
		rate := map[string]float64{"freeze": 4, "slow": 2, "haste": 2, "shield": 3}[e.Status]
		p := rate*e.Duration + e.Radius*0.3
		if e.Target == "enemies" {
			p *= 1.5
		}
		return p, e.Radius, nil
	case "magnet":
		if nested {
			return 0, 0, fmt.Errorf("magnet cannot be nested in on_hit")
		}
		if err = inRange("magnet.radius", e.Radius, 3, 15); err != nil {
			return
		}
		if err = inRange("magnet.strength", e.Strength, 3, 30); err != nil {
			return
		}
		if err = inRange("magnet.duration", e.Duration, 0.5, 4); err != nil {
			return
		}
		return e.Radius*0.4 + e.Strength*0.3 + e.Duration*2, e.Radius, nil
	case "wall":
		if nested {
			return 0, 0, fmt.Errorf("wall cannot be nested in on_hit")
		}
		if err = inRange("wall.length", e.Length, 4, 16); err != nil {
			return
		}
		if err = inRange("wall.duration", e.Duration, 0.5, 4); err != nil {
			return
		}
		if err = inRange("wall.distance", e.Distance, 2, 12); err != nil {
			return
		}
		return e.Length*0.4 + e.Duration*2.5, e.Distance, nil
	}
	return 0, 0, fmt.Errorf("unknown effect type %q (dash_self, impulse_ball, lob_ball, blast, projectile, status, magnet, wall)", e.Type)
}

// CompileSkill validates a spec and derives its economy. Nothing is silently clamped:
// the agent gets an exact error so it can fix the spec (that retry costs real time).
func CompileSkill(spec SkillSpec) (*Skill, error) {
	spec.Name = strings.TrimSpace(spec.Name)
	if spec.Name == "" || len(spec.Name) > 32 {
		return nil, fmt.Errorf("name is required (1..32 chars)")
	}
	if len(spec.Description) > 200 {
		return nil, fmt.Errorf("description max 200 chars")
	}
	if len(spec.Effects) == 0 || len(spec.Effects) > MaxEffects {
		return nil, fmt.Errorf("effects must have 1..%d entries", MaxEffects)
	}
	if err := ValidateVisual(spec.Visual); err != nil {
		return nil, err
	}
	effects := make([]Effect, len(spec.Effects))
	copy(effects, spec.Effects)
	var power, reach float64
	for i := range effects {
		p, r, err := scoreEffect(&effects[i], false)
		if err != nil {
			return nil, fmt.Errorf("effects[%d]: %w", i, err)
		}
		power += p
		if r > reach {
			reach = r
		}
	}
	if power > MaxPower {
		return nil, fmt.Errorf("skill too powerful: score %.1f > cap %.0f (weaken or remove an effect)", power, MaxPower)
	}
	spec.Effects = effects
	return &Skill{
		Spec:       spec,
		Power:      round1(power),
		CreateCost: round1(8 + 1.8*power),
		CastCost:   round1(2 + 0.15*power),
		Cooldown:   round1(2 + 0.35*power),
		Recoil:     round1(0.03 * power),
		Reach:      round1(reach),
	}, nil
}

func round1(v float64) float64 { return float64(int(v*10+0.5)) / 10 }
