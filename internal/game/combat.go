package game

import (
	"fmt"

	"github.com/leoriche/grimhold/internal/ansi"
	"github.com/leoriche/grimhold/internal/dice"
)

// Combat constants.
const (
	// critRoll is the natural d20 that doubles every damage die and lets a
	// foe's rider damage through.
	critRoll = 20
	// fumbleRoll is the natural d20 that always misses, whatever the bonus.
	fumbleRoll = 1
	// fleeBaseDC is the difficulty of slipping away from a foe.
	fleeBaseDC = 10
)

// Attack is the result of one attack roll, kept structured so the combat tests
// can assert on it rather than on printed text.
type Attack struct {
	Attacker string
	Target   string
	Action   string

	Roll     int // the natural d20
	Bonus    int
	Total    int // Roll + Bonus
	TargetAC int

	Hit    bool
	Crit   bool
	Fumble bool

	Damage      int
	TargetHP    int
	TargetMaxHP int
}

// resolveAttack rolls one attack: d20 plus bonus against the target's armour
// class. A natural 1 always misses, a natural 20 always hits and doubles the
// damage dice, and rider damage only lands on that critical.
func resolveAttack(r *dice.Roller, attacker, target, action string, bonus int, damage, rider dice.Expr, targetAC int) Attack {
	a := Attack{
		Attacker: attacker,
		Target:   target,
		Action:   action,
		Roll:     r.D20(),
		Bonus:    bonus,
		TargetAC: targetAC,
	}
	a.Total = a.Roll + a.Bonus

	switch {
	case a.Roll == fumbleRoll:
		a.Fumble = true
	case a.Roll == critRoll:
		a.Hit, a.Crit = true, true
	case a.Total >= targetAC:
		a.Hit = true
	}

	if !a.Hit {
		return a
	}
	if a.Crit {
		a.Damage = damage.RollCrit(r)
		if !rider.IsZero() {
			a.Damage += rider.Roll(r)
		}
	} else {
		a.Damage = damage.Roll(r)
	}
	if a.Damage < 1 {
		a.Damage = 1
	}
	return a
}

// Line renders an attack the way the fight command prints it.
func (a Attack) Line() string {
	roll := fmt.Sprintf("d20(%d)+%d = %d vs AC %d", a.Roll, a.Bonus, a.Total, a.TargetAC)
	switch {
	case a.Fumble:
		return fmt.Sprintf("%s swings %s and fumbles. %s", a.Attacker, a.Action, ansi.Gray(roll))
	case a.Crit:
		return fmt.Sprintf("%s %s with %s for %s damage. %s",
			a.Attacker, ansi.Yellow("CRITS"), a.Action, ansi.Bold(fmt.Sprintf("%d", a.Damage)), ansi.Gray(roll))
	case a.Hit:
		return fmt.Sprintf("%s hits with %s for %d damage. %s", a.Attacker, a.Action, a.Damage, ansi.Gray(roll))
	default:
		return fmt.Sprintf("%s misses with %s. %s", a.Attacker, a.Action, ansi.Gray(roll))
	}
}

// Round is one exchange of blows. FoeAttack is nil when the foe went down
// before it could strike back.
type Round struct {
	Number     int
	PlayerHit  Attack
	FoeAttack  *Attack
	FoeDown    bool
	PlayerDown bool
}

// Lines renders the round for the terminal.
func (rd *Round) Lines(foeName, playerName string, foeHP, foeMaxHP, playerHP, playerMaxHP int) []string {
	lines := []string{
		ansi.Dim(fmt.Sprintf("-- round %d --", rd.Number)),
		"  " + rd.PlayerHit.Line(),
	}
	if rd.FoeDown {
		lines = append(lines, "  "+ansi.Green(fmt.Sprintf("%s falls.", foeName)))
		return lines
	}
	lines = append(lines, fmt.Sprintf("  %s %s", foeName, ansi.HP(foeHP, foeMaxHP, 14)))
	if rd.FoeAttack != nil {
		lines = append(lines, "  "+rd.FoeAttack.Line())
	}
	if rd.PlayerDown {
		lines = append(lines, "  "+ansi.Red(fmt.Sprintf("%s goes down.", playerName)))
		return lines
	}
	lines = append(lines, fmt.Sprintf("  %s %s", playerName, ansi.HP(playerHP, playerMaxHP, 14)))
	return lines
}

// FightRound resolves one round: the player strikes, then the foe strikes back
// if it is still standing. It only moves hit points around; awarding
// experience and handling death are the caller's job, which keeps the engine
// easy to test.
func (g *Game) FightRound() (*Round, error) {
	if g.Foe == nil {
		return nil, ErrNoEncounter
	}
	if !g.Char.Alive() {
		return nil, ErrDead
	}

	g.RoundNumber++
	rd := &Round{Number: g.RoundNumber}

	rd.PlayerHit = resolveAttack(g.Roller, g.Char.Name, g.Foe.Name, g.Char.Weapon.Name,
		g.Char.AttackBonus, g.Char.DamageExpr(), dice.Expr{}, g.Foe.AC)
	if rd.PlayerHit.Hit {
		g.Foe.Hurt(rd.PlayerHit.Damage)
	}
	rd.PlayerHit.TargetHP, rd.PlayerHit.TargetMaxHP = g.Foe.HP, g.Foe.MaxHP

	if !g.Foe.Alive() {
		rd.FoeDown = true
		return rd, nil
	}

	foeHit := resolveAttack(g.Roller, g.Foe.Name, g.Char.Name, g.Foe.AttackName,
		g.Foe.AttackBonus, g.Foe.damage, g.Foe.rider, g.Char.ArmorClass())
	if foeHit.Hit {
		g.Char.Hurt(foeHit.Damage)
	}
	foeHit.TargetHP, foeHit.TargetMaxHP = g.Char.HP, g.Char.MaxHP
	rd.FoeAttack = &foeHit
	rd.PlayerDown = !g.Char.Alive()
	return rd, nil
}

// FleeResult is the outcome of trying to run away.
type FleeResult struct {
	Roll       int
	Bonus      int
	Total      int
	DC         int
	Escaped    bool
	FreeHit    *Attack
	PlayerDown bool
}

// Flee attempts to break away from the current encounter: one dexterity-style
// roll against a difficulty set by the foe's challenge rating. Failing it
// gives the foe a free hit.
func (g *Game) Flee() (*FleeResult, error) {
	if g.Foe == nil {
		return nil, ErrNoEncounter
	}
	if !g.Char.Alive() {
		return nil, ErrDead
	}

	res := &FleeResult{
		Roll:  g.Roller.D20(),
		Bonus: 2 + g.Char.Level/2,
		DC:    fleeBaseDC + int(g.Foe.CR/2),
	}
	if g.Foe.Boss {
		res.DC += 3
	}
	res.Total = res.Roll + res.Bonus
	res.Escaped = res.Total >= res.DC

	if res.Escaped {
		return res, nil
	}

	hit := resolveAttack(g.Roller, g.Foe.Name, g.Char.Name, g.Foe.AttackName,
		g.Foe.AttackBonus, g.Foe.damage, g.Foe.rider, g.Char.ArmorClass())
	if hit.Hit {
		g.Char.Hurt(hit.Damage)
	}
	hit.TargetHP, hit.TargetMaxHP = g.Char.HP, g.Char.MaxHP
	res.FreeHit = &hit
	res.PlayerDown = !g.Char.Alive()
	return res, nil
}

// Lines renders a flee attempt.
func (res *FleeResult) Lines(foeName, playerName string, playerHP, playerMaxHP int) []string {
	roll := ansi.Gray(fmt.Sprintf("d20(%d)+%d = %d vs DC %d", res.Roll, res.Bonus, res.Total, res.DC))
	if res.Escaped {
		return []string{
			fmt.Sprintf("You throw yourself backwards into the dark and the %s loses you. %s", foeName, roll),
		}
	}
	lines := []string{fmt.Sprintf("You turn to run and stumble. %s", roll)}
	if res.FreeHit != nil {
		lines = append(lines, "  "+res.FreeHit.Line())
	}
	if res.PlayerDown {
		lines = append(lines, "  "+ansi.Red(fmt.Sprintf("%s goes down.", playerName)))
		return lines
	}
	lines = append(lines, fmt.Sprintf("  %s %s", playerName, ansi.HP(playerHP, playerMaxHP, 14)))
	return lines
}
