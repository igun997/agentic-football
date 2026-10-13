package sim

import "testing"

func briefFor(archetype, element string, budget float64, intensity string) Brief {
	b := DefaultBrief()
	b.Persona.PreferredRole = RoleMID
	b.Skills = []SkillBrief{{Slot: 0, Concept: "a thing", Archetype: archetype, Element: element, Intensity: intensity, PowerBudget: budget}}
	return b
}

func visual(color string, ms int) Visual {
	v := goodVisual()
	v.Color, v.DurationMs = color, ms
	return v
}

func TestBriefNormalize(t *testing.T) {
	b := briefFor("attack", "fire", 0, "")
	if err := b.Normalize(); err != nil || b.Skills[0].Color != "#ff6a2b" || b.Skills[0].PowerBudget != 25 || b.Skills[0].Intensity != "medium" {
		t.Fatalf("defaults/derived color wrong: %v %+v", err, b.Skills[0])
	}
	bad := []Brief{
		briefFor("nuke", "fire", 20, "low"),
		briefFor("attack", "plasma", 20, "low"),
		briefFor("attack", "fire", 99, "low"),
		briefFor("attack", "custom", 20, "low"), // custom needs a colour
	}
	for i, x := range bad {
		if err := x.Normalize(); err == nil {
			t.Errorf("bad brief %d accepted", i)
		}
	}
	dirty := DefaultBrief()
	dirty.Persona.Motto = "  <b>go</b>\n\n  team  "
	_ = dirty.Normalize()
	if dirty.Persona.Motto != "bgo/b team" {
		t.Fatalf("motto not sanitised: %q", dirty.Persona.Motto)
	}
}

func TestForgeFollowsBrief(t *testing.T) {
	w := NewWorld(60, 5)
	a := w.AddAgent("a1", "A", 0)
	b := briefFor("attack", "fire", 20, "low")
	if err := b.Normalize(); err != nil {
		t.Fatal(err)
	}
	w.ApplyBrief(a, &b)
	if a.Body.Role != RoleMID {
		t.Fatalf("agent should start on its preferred position, got %s", a.Body.Role)
	}
	ok := SkillSpec{Name: "Ember", Effects: []Effect{{Type: "blast", Radius: 8, Power: 15, Targets: []string{"ball"}}}, Visual: visual("#ff8030", 900)}
	if sk, e := w.ForgeSkill(a, 0, ok); e != nil || a.Skill[0] != sk || a.CompileLeft != 0 {
		t.Fatalf("on-brief forge must be free and instant: %v", e)
	}
	cases := map[string]struct {
		slot int
		spec SkillSpec
		code string
	}{
		"unbriefed slot":  {1, ok, "no_brief"},
		"wrong archetype": {0, SkillSpec{Name: "Ice", Effects: []Effect{{Type: "wall", Length: 6, Duration: 1, Distance: 4}}, Visual: visual("#ff8030", 900)}, "guardrail"},
		"off-theme color": {0, SkillSpec{Name: "Ember", Effects: ok.Effects, Visual: visual("#3060ff", 900)}, "guardrail"},
		"missing color":   {0, SkillSpec{Name: "Ember", Effects: ok.Effects, Visual: visual("", 900)}, "guardrail"},
		"too long vfx":    {0, SkillSpec{Name: "Ember", Effects: ok.Effects, Visual: visual("#ff8030", 2500)}, "guardrail"},
		"over budget":     {0, SkillSpec{Name: "Ember", Effects: []Effect{{Type: "blast", Radius: 20, Power: 40, Targets: []string{"ball"}}}, Visual: visual("#ff8030", 900)}, "guardrail"},
		"nothing drawn":   {0, SkillSpec{Name: "Ember", Effects: ok.Effects, Visual: Visual{Code: "return {}", DurationMs: 900, Color: "#ff8030"}}, "invalid_skill"},
	}
	for name, c := range cases {
		if _, e := w.ForgeSkill(a, c.slot, c.spec); e == nil || e.Code != c.code {
			t.Errorf("%s: want %s, got %v", name, c.code, e)
		}
	}
	w.Start()
	for i := 0; i < int((CountdownSecs+RestartWait+0.5)*TickRate); i++ {
		w.Step()
	}
	if _, e := w.ForgeSkill(a, 0, ok); e == nil || e.Code != "not_prematch" {
		t.Fatalf("forge during the match must fail: %v", e)
	}
}

func TestLiveForgingRespectsTrainer(t *testing.T) {
	w := NewWorld(60, 5)
	a := w.AddAgent("a1", "A", 0)
	w.AddAgent("a2", "B", 1)
	b := briefFor("attack", "fire", 20, "low")
	_ = b.Normalize()
	w.ApplyBrief(a, &b)
	spec := SkillSpec{Name: "Ember", Effects: []Effect{{Type: "blast", Radius: 8, Power: 15, Targets: []string{"ball"}}}, Visual: visual("#ff8030", 900)}
	if _, e := w.CreateSkill(a, 0, spec); e == nil || e.Code != "prematch" {
		t.Fatalf("create_skill before kickoff must point to forge_skill: %v", e)
	}
	startPlay(t, w)
	bad := spec
	bad.Visual = visual("#3060ff", 900)
	if _, e := w.CreateSkill(a, 0, bad); e == nil || e.Code != "guardrail" {
		t.Fatalf("live forging must still follow the brief: %v", e)
	}
	if _, e := w.CreateSkill(a, 0, spec); e != nil {
		t.Fatalf("on-brief live forge failed: %v", e)
	}
	a.CompileLeft = 0
	b.LockLoadout = true
	if _, e := w.CreateSkill(a, 1, spec); e == nil || e.Code != "loadout_locked" {
		t.Fatalf("locked loadout must stop live forging: %v", e)
	}
}
