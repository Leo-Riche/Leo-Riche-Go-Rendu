package game

import (
	"errors"
	"strings"
	"testing"

	"github.com/leoriche/grimhold/internal/dice"
)

// seedWithFirstD20 finds a seed whose very first d20 is the wanted face.
// resolveAttack rolls the attack die first, so a roller built from that seed
// makes the next attack land on exactly that number. That is how these tests
// pin down critical hits and fumbles without a fake random source.
func seedWithFirstD20(t *testing.T, want int) int64 {
	t.Helper()
	for seed := int64(1); seed < 1_000_000; seed++ {
		if dice.New(seed).D20() == want {
			return seed
		}
	}
	t.Fatalf("no seed in range rolls a %d first", want)
	return 0
}

func TestResolveAttackInvariants(t *testing.T) {
	damage := dice.MustParseExpr("1d6+2")

	for seed := int64(1); seed <= 500; seed++ {
		r := dice.New(seed)
		for i := 0; i < 20; i++ {
			got := resolveAttack(r, "Player", "Foe", "sword", 4, damage, dice.Expr{}, 15)

			if got.Roll < 1 || got.Roll > 20 {
				t.Fatalf("seed %d: d20 = %d", seed, got.Roll)
			}
			if got.Total != got.Roll+got.Bonus {
				t.Fatalf("seed %d: total %d != roll %d + bonus %d", seed, got.Total, got.Roll, got.Bonus)
			}

			wantHit := got.Roll == 20 || (got.Roll != 1 && got.Total >= got.TargetAC)
			if got.Hit != wantHit {
				t.Fatalf("seed %d: roll %d total %d vs AC %d gave hit=%v", seed, got.Roll, got.Total, got.TargetAC, got.Hit)
			}
			if got.Crit != (got.Roll == 20) {
				t.Fatalf("seed %d: crit=%v on a roll of %d", seed, got.Crit, got.Roll)
			}
			if got.Fumble != (got.Roll == 1) {
				t.Fatalf("seed %d: fumble=%v on a roll of %d", seed, got.Fumble, got.Roll)
			}
			switch {
			case got.Hit && got.Damage < 1:
				t.Fatalf("seed %d: a hit dealt %d damage", seed, got.Damage)
			case !got.Hit && got.Damage != 0:
				t.Fatalf("seed %d: a miss dealt %d damage", seed, got.Damage)
			}
		}
	}
}

func TestNaturalTwentyAlwaysHitsAndDoublesTheDice(t *testing.T) {
	seed := seedWithFirstD20(t, 20)
	// An absurd armour class, so only a critical can land.
	got := resolveAttack(dice.New(seed), "Player", "Foe", "sword", 0, dice.MustParseExpr("1d6+100"), dice.Expr{}, 999)

	if !got.Hit || !got.Crit {
		t.Fatalf("a natural 20 must hit and crit, got %+v", got)
	}
	// Two d6 plus the untouched flat 100.
	if got.Damage < 102 || got.Damage > 112 {
		t.Errorf("crit damage = %d, want 1d6 doubled plus 100", got.Damage)
	}
	if !strings.Contains(got.Line(), "CRITS") {
		t.Errorf("crit line = %q", got.Line())
	}
}

func TestNaturalOneAlwaysMisses(t *testing.T) {
	seed := seedWithFirstD20(t, 1)
	got := resolveAttack(dice.New(seed), "Player", "Foe", "sword", 100, dice.MustParseExpr("1d6"), dice.Expr{}, 2)

	if got.Hit || got.Damage != 0 {
		t.Fatalf("a natural 1 must miss whatever the bonus, got %+v", got)
	}
	if !strings.Contains(got.Line(), "fumble") {
		t.Errorf("fumble line = %q", got.Line())
	}
}

func TestRiderDamageOnlyLandsOnACritical(t *testing.T) {
	damage := dice.MustParseExpr("1d1") // always 1
	rider := dice.MustParseExpr("10d1") // always 10

	crit := resolveAttack(dice.New(seedWithFirstD20(t, 20)), "Foe", "Player", "fist", 0, damage, rider, 999)
	if crit.Damage != 12 {
		t.Errorf("critical damage = %d, want 2 (doubled 1d1) plus 10 rider", crit.Damage)
	}

	// Any ordinary hit must leave the rider out: roll 19 beats AC 2.
	plain := resolveAttack(dice.New(seedWithFirstD20(t, 19)), "Foe", "Player", "fist", 0, damage, rider, 2)
	if !plain.Hit {
		t.Fatal("a 19 against AC 2 should hit")
	}
	if plain.Damage != 1 {
		t.Errorf("ordinary hit damage = %d, want 1 with no rider", plain.Damage)
	}
}

func TestFightRoundNeedsAnEncounter(t *testing.T) {
	g := newTestGame(t, 1)

	if _, err := g.FightRound(); !errors.Is(err, ErrNoEncounter) {
		t.Errorf("FightRound with no foe returned %v, want ErrNoEncounter", err)
	}
	if _, err := g.Fight(false); !errors.Is(err, ErrNoEncounter) {
		t.Errorf("Fight with no foe returned %v, want ErrNoEncounter", err)
	}

	mustEncounter(t, g)
	g.Char.HP = 0
	if _, err := g.FightRound(); !errors.Is(err, ErrDead) {
		t.Errorf("FightRound while down returned %v, want ErrDead", err)
	}
}

func TestFightRoundMovesHitPointsAndCountsRounds(t *testing.T) {
	g := newTestGame(t, 8)
	foe := mustEncounter(t, g)
	// An ogre survives long enough to be hit several times.
	foe.HP, foe.MaxHP = 200, 200

	for i := 1; i <= 5; i++ {
		before := foe.HP
		rd, err := g.FightRound()
		if err != nil {
			t.Fatalf("round %d returned %v", i, err)
		}
		if rd.Number != i {
			t.Errorf("round number = %d, want %d", rd.Number, i)
		}
		if rd.PlayerHit.Hit && foe.HP >= before {
			t.Errorf("round %d: the foe was hit but kept %d HP", i, foe.HP)
		}
		if foe.HP < 0 || g.Char.HP < 0 {
			t.Fatalf("round %d: hit points went negative (%d / %d)", i, foe.HP, g.Char.HP)
		}
	}
}

func TestFightAllEndsWithExactlyOneSideStanding(t *testing.T) {
	for seed := int64(1); seed <= 30; seed++ {
		g := newTestGame(t, seed)
		mustEncounter(t, g)
		foeName := g.Foe.Name

		lines, err := g.Fight(true)
		if err != nil {
			t.Fatalf("seed %d: fight all returned %v", seed, err)
		}
		if g.Foe != nil {
			t.Fatalf("seed %d: the fight ended with a foe still set", seed)
		}

		joined := strings.Join(lines, "\n")
		playerWon := strings.Contains(joined, foeName+" is dead")
		playerLost := strings.Contains(joined, "leaves you for dead")
		if playerWon == playerLost {
			t.Fatalf("seed %d: unclear outcome:\n%s", seed, joined)
		}
		if playerWon && g.Char.XP == 0 {
			t.Errorf("seed %d: a win awarded no experience", seed)
		}
		if playerWon && len(g.Slain) == 0 {
			t.Errorf("seed %d: a win recorded no kill", seed)
		}
		if playerLost {
			if g.Char.HP != 1 {
				t.Errorf("seed %d: HP after defeat = %d, want 1", seed, g.Char.HP)
			}
			if g.Location != g.World.Start {
				t.Errorf("seed %d: defeat left the player in %q", seed, g.Location)
			}
		}
	}
}

func TestVictoryAwardsXPGoldAndAKill(t *testing.T) {
	g := newTestGame(t, 1)
	g.Foe = &Foe{Slug: "ogre", Name: "Ogre", HP: 0, MaxHP: 59, CR: 2, CRLabel: "2", XP: XPForCR(2)}
	goldBefore := g.Char.Gold

	lines := g.victory()

	if g.Foe != nil {
		t.Error("victory must clear the encounter")
	}
	if g.Char.XP != 450 {
		t.Errorf("XP = %d, want 450 for a CR 2 kill", g.Char.XP)
	}
	if g.Char.Gold <= goldBefore {
		t.Errorf("gold = %d, want more than %d", g.Char.Gold, goldBefore)
	}
	if g.Slain["ogre"] == nil || g.Slain["ogre"].Count != 1 {
		t.Errorf("slain bestiary = %+v", g.Slain)
	}
	if !strings.Contains(strings.Join(lines, "\n"), "level 3") {
		t.Errorf("450 XP should carry a level 1 character to level 3:\n%s", strings.Join(lines, "\n"))
	}
}

func TestDefeatResetsWithoutTouchingTheSave(t *testing.T) {
	g := newTestGame(t, 1)
	g.Char.Gold = 100
	g.Char.HP = 0
	g.Location = "marsh"
	g.Rested = true
	g.Looted = true
	g.Foe = &Foe{Name: "Ogre"}

	lines := g.defeat()

	switch {
	case g.Char.HP != 1:
		t.Errorf("HP = %d, want 1", g.Char.HP)
	case g.Location != "crossroads":
		t.Errorf("location = %q, want the start region", g.Location)
	case g.Foe != nil:
		t.Error("defeat must clear the encounter")
	case g.Rested, g.Looted:
		t.Error("defeat must reset the rest and loot flags")
	case g.Char.Gold != 90:
		t.Errorf("gold = %d, want 90 after losing a tenth", g.Char.Gold)
	}
	if !strings.Contains(strings.Join(lines, " "), "save file is untouched") {
		t.Errorf("defeat should reassure the player about the save: %v", lines)
	}
}

func TestFleeEscapesOrTakesAFreeHit(t *testing.T) {
	escaped, hit := 0, 0

	for seed := int64(1); seed <= 60; seed++ {
		g := newTestGame(t, seed)
		mustEncounter(t, g)
		hpBefore := g.Char.HP

		res, err := g.Flee()
		if err != nil {
			t.Fatalf("seed %d: Flee returned %v", seed, err)
		}
		if res.Total != res.Roll+res.Bonus {
			t.Fatalf("seed %d: flee total is inconsistent: %+v", seed, res)
		}
		if res.Escaped != (res.Total >= res.DC) {
			t.Fatalf("seed %d: escaped=%v for %d vs DC %d", seed, res.Escaped, res.Total, res.DC)
		}

		if res.Escaped {
			escaped++
			if res.FreeHit != nil {
				t.Errorf("seed %d: a clean escape must not take a hit", seed)
			}
			continue
		}
		hit++
		if res.FreeHit == nil {
			t.Fatalf("seed %d: a failed flee must give the foe a free hit", seed)
		}
		if res.FreeHit.Hit && g.Char.HP >= hpBefore {
			t.Errorf("seed %d: the free hit landed but cost no hit points", seed)
		}
	}

	if escaped == 0 || hit == 0 {
		t.Errorf("60 flee attempts gave %d escapes and %d free hits; both should happen", escaped, hit)
	}
}

func TestRunAwayClearsTheEncounterOnlyOnEscape(t *testing.T) {
	g := newTestGame(t, 1)
	if _, err := g.RunAway(); !errors.Is(err, ErrNoEncounter) {
		t.Errorf("RunAway with no foe returned %v, want ErrNoEncounter", err)
	}

	for seed := int64(1); seed <= 40; seed++ {
		g := newTestGame(t, seed)
		mustEncounter(t, g)

		lines, err := g.RunAway()
		if err != nil {
			t.Fatalf("seed %d: RunAway returned %v", seed, err)
		}
		joined := strings.Join(lines, "\n")
		escaped := strings.Contains(joined, "loses you")
		if escaped != (g.Foe == nil) {
			t.Fatalf("seed %d: escape text and game state disagree:\n%s", seed, joined)
		}
	}
}

func TestFleeIsHarderAgainstABoss(t *testing.T) {
	plain := &Foe{Name: "Ogre", CR: 2, DamageDice: "1d6", AttackName: "club"}
	boss := &Foe{Name: "Mummy Lord", CR: 2, DamageDice: "1d6", AttackName: "fist", Boss: true}
	if err := plain.prepare(); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if err := boss.prepare(); err != nil {
		t.Fatalf("prepare: %v", err)
	}

	g := newTestGame(t, 1)
	g.Foe = plain
	plainRes, err := g.Flee()
	if err != nil {
		t.Fatalf("Flee returned %v", err)
	}

	g = newTestGame(t, 1)
	g.Foe = boss
	bossRes, err := g.Flee()
	if err != nil {
		t.Fatalf("Flee returned %v", err)
	}

	if bossRes.DC != plainRes.DC+3 {
		t.Errorf("boss flee DC = %d, want %d", bossRes.DC, plainRes.DC+3)
	}
}

func TestAttackLineReadsBack(t *testing.T) {
	hit := Attack{Attacker: "Tester", Target: "Goblin", Action: "sword", Roll: 14, Bonus: 4, Total: 18, TargetAC: 15, Hit: true, Damage: 6}
	if got := hit.Line(); !strings.Contains(got, "hits") || !strings.Contains(got, "6 damage") || !strings.Contains(got, "vs AC 15") {
		t.Errorf("hit line = %q", got)
	}

	miss := Attack{Attacker: "Goblin", Target: "Tester", Action: "scimitar", Roll: 5, Bonus: 4, Total: 9, TargetAC: 13}
	if got := miss.Line(); !strings.Contains(got, "misses") {
		t.Errorf("miss line = %q", got)
	}
}
