package sim

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
)

// A Brief is the "trainer setup" a human writes for their agent before the match (Pokémon style):
// persona, preferred position and up to three skill concepts. The agent has to forge skills that
// fit the brief; Check() is the MCP guardrail that enforces it.

type Persona struct {
	Style         string `json:"style"`          // balanced | aggressive | defensive | trickster
	PreferredRole Role   `json:"preferred_role"` // GK | DEF | MID | FWD
	Motto         string `json:"motto"`
}

type SkillBrief struct {
	Slot        int     `json:"slot"`
	Concept     string  `json:"concept"`      // creative direction in the trainer's words
	Archetype   string  `json:"archetype"`    // attack | defense | control | mobility | trick
	Element     string  `json:"element"`      // fire | ice | storm | void | nature | cosmic | custom
	Color       string  `json:"color"`        // #rrggbb, derived from element unless custom
	Intensity   string  `json:"intensity"`    // low | medium | high (visual budget)
	PowerBudget float64 `json:"power_budget"` // max power score P for this skill
}

type Brief struct {
	Persona     Persona      `json:"persona"`
	Skills      []SkillBrief `json:"skills"`
	LockLoadout bool         `json:"lock_loadout"` // true: no live forging during the match
}

var Elements = map[string]string{
	"fire": "#ff6a2b", "ice": "#66ddff", "storm": "#ffe14a", "void": "#b26bff", "nature": "#6bff8a", "cosmic": "#ff6bd6",
}

var archetypeAllowed = map[string][]string{
	"attack":   {"impulse_ball", "blast", "projectile", "dash_self"},
	"defense":  {"wall", "status", "blast"},
	"control":  {"status", "magnet", "wall", "projectile"},
	"mobility": {"dash_self", "status", "impulse_ball"},
	"trick":    {"dash_self", "impulse_ball", "blast", "projectile", "status", "magnet", "wall"},
}

var intensityLimits = map[string]struct{ Ms, Chars int }{
	"low": {1200, 2000}, "medium": {2000, 4000}, "high": {3000, 6000},
}

const hueTolerance = 40.0

// GuardRails describes the limits of one skill brief in agent-readable form.
func (sb *SkillBrief) GuardRails() map[string]any {
	lim := intensityLimits[sb.Intensity]
	return map[string]any{
		"allowed_effect_types": archetypeAllowed[sb.Archetype],
		"max_power":            sb.PowerBudget,
		"max_visual_ms":        lim.Ms,
		"max_visual_chars":     lim.Chars,
		"visual_color":         sb.Color,
		"visual_color_rule":    fmt.Sprintf("visual.color must be a #rrggbb within ±%.0f° hue of %s", hueTolerance, sb.Color),
		"visual_must_call":     "ctx.group.add(...)",
		"mobility_rule":        "for archetype mobility, status effects must target self",
	}
}

func DefaultBrief() Brief {
	return Brief{Persona: Persona{Style: "balanced", PreferredRole: RoleFWD}, Skills: []SkillBrief{}}
}

func (b *Brief) SkillFor(slot int) *SkillBrief {
	for i := range b.Skills {
		if b.Skills[i].Slot == slot {
			return &b.Skills[i]
		}
	}
	return nil
}

// sanitize keeps printable text only, collapses whitespace and limits the rune count.
func sanitize(s string, max int) string {
	var sb strings.Builder
	space := false
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			space = true
		case unicode.IsControl(r) || r == '<' || r == '>':
		default:
			if space && sb.Len() > 0 {
				sb.WriteByte(' ')
			}
			space = false
			sb.WriteRune(r)
		}
	}
	out := []rune(sb.String())
	if len(out) > max {
		out = out[:max]
	}
	return string(out)
}

func oneIn(v string, allowed ...string) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}

// Normalize validates and cleans a brief that came from the human trainer.
func (b *Brief) Normalize() error {
	if b.Skills == nil {
		b.Skills = []SkillBrief{}
	}
	if b.Persona.Style == "" {
		b.Persona.Style = "balanced"
	}
	if !oneIn(b.Persona.Style, "balanced", "aggressive", "defensive", "trickster") {
		return fmt.Errorf("persona.style must be balanced|aggressive|defensive|trickster")
	}
	if b.Persona.PreferredRole == "" {
		b.Persona.PreferredRole = RoleFWD
	}
	if !oneIn(string(b.Persona.PreferredRole), "GK", "DEF", "MID", "FWD") {
		return fmt.Errorf("persona.preferred_role must be GK|DEF|MID|FWD")
	}
	b.Persona.Motto = sanitize(b.Persona.Motto, 60)
	if len(b.Skills) > MaxSkillSlots {
		return fmt.Errorf("at most %d skill briefs", MaxSkillSlots)
	}
	seen := map[int]bool{}
	for i := range b.Skills {
		sb := &b.Skills[i]
		if sb.Slot < 0 || sb.Slot >= MaxSkillSlots || seen[sb.Slot] {
			return fmt.Errorf("skills[%d]: slot must be a unique 0..%d", i, MaxSkillSlots-1)
		}
		seen[sb.Slot] = true
		sb.Concept = sanitize(sb.Concept, 140)
		if len([]rune(sb.Concept)) < 3 {
			return fmt.Errorf("skills[%d]: concept needs 3..140 chars", i)
		}
		if _, ok := archetypeAllowed[sb.Archetype]; !ok {
			return fmt.Errorf("skills[%d]: archetype must be attack|defense|control|mobility|trick", i)
		}
		if c, ok := Elements[sb.Element]; ok {
			sb.Color = c
		} else if sb.Element == "custom" {
			if _, err := hexHue(sb.Color); err != nil {
				return fmt.Errorf("skills[%d]: custom element needs color #rrggbb", i)
			}
			sb.Color = strings.ToLower(sb.Color)
		} else {
			return fmt.Errorf("skills[%d]: element must be fire|ice|storm|void|nature|cosmic|custom", i)
		}
		if sb.Intensity == "" {
			sb.Intensity = "medium"
		}
		if _, ok := intensityLimits[sb.Intensity]; !ok {
			return fmt.Errorf("skills[%d]: intensity must be low|medium|high", i)
		}
		if sb.PowerBudget == 0 {
			sb.PowerBudget = 25
		}
		if sb.PowerBudget < 8 || sb.PowerBudget > MaxPower {
			return fmt.Errorf("skills[%d]: power_budget must be 8..%.0f", i, MaxPower)
		}
	}
	return nil
}

// hexHue returns hue in degrees and saturation for #rrggbb.
func hexHue(hex string) (float64, error) {
	h, _, err := hexHS(hex)
	return h, err
}

func hexHS(hex string) (hue, sat float64, err error) {
	hex = strings.TrimPrefix(strings.TrimSpace(hex), "#")
	if len(hex) != 6 {
		return 0, 0, fmt.Errorf("bad color")
	}
	v, e := strconv.ParseUint(hex, 16, 32)
	if e != nil {
		return 0, 0, fmt.Errorf("bad color")
	}
	r, g, b := float64(v>>16&255)/255, float64(v>>8&255)/255, float64(v&255)/255
	mx, mn := math.Max(r, math.Max(g, b)), math.Min(r, math.Min(g, b))
	d := mx - mn
	if mx > 0 {
		sat = d / mx
	}
	switch {
	case d == 0:
		hue = 0
	case mx == r:
		hue = math.Mod((g-b)/d, 6)
	case mx == g:
		hue = (b-r)/d + 2
	default:
		hue = (r-g)/d + 4
	}
	hue *= 60
	if hue < 0 {
		hue += 360
	}
	return hue, sat, nil
}

// Check is the guardrail: does a compiled skill respect this brief?
func (sb *SkillBrief) Check(spec SkillSpec, power float64) error {
	if power > sb.PowerBudget+1e-9 {
		return fmt.Errorf("power %.1f exceeds the trainer's budget %.0f for this slot", power, sb.PowerBudget)
	}
	allowed := archetypeAllowed[sb.Archetype]
	for i, e := range spec.Effects {
		if !oneIn(e.Type, allowed...) {
			return fmt.Errorf("effects[%d]: %q is not allowed for archetype %q (allowed: %v)", i, e.Type, sb.Archetype, allowed)
		}
		if sb.Archetype == "mobility" && e.Type == "status" && e.Target != "self" {
			return fmt.Errorf("effects[%d]: mobility skills may only target self", i)
		}
	}
	lim := intensityLimits[sb.Intensity]
	if spec.Visual.DurationMs > lim.Ms {
		return fmt.Errorf("visual.duration_ms %d exceeds %d for intensity %q", spec.Visual.DurationMs, lim.Ms, sb.Intensity)
	}
	if len(spec.Visual.Code) > lim.Chars {
		return fmt.Errorf("visual.code is %d chars, intensity %q allows %d", len(spec.Visual.Code), sb.Intensity, lim.Chars)
	}
	h, s, err := hexHS(spec.Visual.Color)
	if err != nil {
		return fmt.Errorf("visual.color is required (#rrggbb) and must follow the brief color %s", sb.Color)
	}
	bh, bs, _ := hexHS(sb.Color)
	if bs > 0.2 && s > 0.2 {
		d := math.Abs(h - bh)
		if d > 180 {
			d = 360 - d
		}
		if d > hueTolerance {
			return fmt.Errorf("visual.color %s is off-theme: brief color is %s (element %s)", spec.Visual.Color, sb.Color, sb.Element)
		}
	}
	return nil
}
