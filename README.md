# Grimhold

A text-based dungeon crawler for the terminal. You explore cursed regions,
stumble into random encounters, fight them out with dice, and keep a bestiary
of everything you kill.

Every monster, weapon and spell comes live from the [Open5e
API](https://api.open5e.com) (D&D 5e SRD content, free, no API key). The map is
a local `world.json`. Standard library only: no third-party dependency, in the
core or in the tests.

```text
grimhold [Ashen Marsh 30/30] > explore
Encounter! Goblin comes out of the dark.
  Goblin  CR 1/4  AC 15  [##############] 7/7
  attacks with Scimitar at d20+4 for 1d6+2
fight to trade blows, fight all to see it through, flee to run.
```

## Build and run

Go 1.22 or newer.

```bash
go build -o grimhold ./cmd/grimhold
```

```bash
./grimhold
```

Run it from the repository root, or keep `world.json` next to the binary: the
game looks for the map in the working directory first, then beside the
executable, and `-world <path>` overrides both.

Without a build:

```bash
go run ./cmd/grimhold
```

Resume the last save:

```bash
go run ./cmd/grimhold -continue
```

### Flags

| Flag | Default | What it does |
|---|---|---|
| `-world <path>` | `./world.json` | Map file to load |
| `-save <path>` | `save.json` | File used by `save` and `load` |
| `-continue` | off | Load the save file before starting |
| `-name <name>` | asked at startup | Character name |
| `-no-spells` | off | Skip choosing spells at creation |
| `-no-color` | off | Disable ANSI colour (so does `NO_COLOR=1`) |
| `-seed <n>` | random | Fix the dice seed for a reproducible session |
| `-ambush <duration>` | `25s` | How often an ambush may interrupt the prompt (`0` disables) |
| `-cache-ttl <duration>` | `5m` | How long an Open5e response stays fresh |
| `-cache-reap <duration>` | `1m` | How often the cache reaper purges expired entries |

At startup the game asks for a name and offers nine spells to pick two from.
When standard input is not a terminal (a piped script, a test) those questions
are skipped and the defaults are taken, so a scripted session is never thrown
off by a prompt.

## Commands

`help` is generated from the command table itself, so it can never drift from
what the program actually accepts.

| Command | What it does |
|---|---|
| `help` | Show the command list |
| `map` | List the known regions, their danger and their exits |
| `travel <region>` | Travel to a region connected to this one |
| `look` | Describe your surroundings |
| `explore` | Wander the region and risk an encounter |
| `fight` / `fight all` | Trade one round of blows, or fight to the finish |
| `flee` | Try to break away (a failed roll costs you a free hit) |
| `rest` | Heal fully and recharge spells, once per region visit |
| `character` | Print your character sheet |
| `loot` | Pick over the region for gold and weapons, once per visit |
| `weapons` | List what you carry and every melee weapon in the API |
| `equip <weapon>` | Take a weapon out of your pack |
| `spells` / `cast <spell>` | List your spells, cast one (once per rest) |
| `bestiary [next\|prev]` | Page through the Open5e monster list |
| `inspect <monster>` | Print a monster stat block from Open5e |
| `slain` | Show your bestiary of kills |
| `cache` | Show the state of the Open5e response cache |
| `save [file]` / `load [file]` | Write or restore the game state |
| `clear` | Clear the screen |
| `quit` | Leave (your save is not written for you) |

Shortcuts: `h`/`?`, `m`, `t`/`go`, `l`, `e`, `f`/`attack`, `r`, `c`/`char`/`sheet`,
`b`, `i`, `kills`, `run`, `q`/`exit`.

`fight` resolves a single round on purpose, so that `flee` and `cast` are real
choices mid-fight; `fight all` runs the fight to its end in one command.

## How it is put together

```text
cmd/grimhold/main.go     flags, world loading, character creation, wiring
internal/repl            the read-eval-print loop and the command table
internal/game            character, foes, combat, encounters, save/load
internal/open5e          typed API client, pagination, TTL cache + reaper
internal/world           world.json loading and validation
internal/dice            d20s and "2d6+3" expressions, seedable
internal/ansi            hand-rolled escape codes, HP bars, danger pips
world.json               the map
```

`main` stays thin: it parses flags, loads the map, builds the client and the
game, and hands them to the REPL. Errors are wrapped with `fmt.Errorf("...:
%w", err)` all the way up and none are dropped silently.

### The REPL never dies

- Commands live in a `map[string]cliCommand`, each entry carrying its name,
  usage, description and callback. `help` and the alias list are rendered from
  that map.
- Unknown commands, missing arguments, extra arguments, empty lines and
  whitespace-only lines all produce a message and a new prompt.
- Impossible moves (travelling with no road, resting twice in one region,
  fighting nothing) are errors, not panics.
- `dispatch` recovers from a panic inside a command, prints it and carries on,
  so a bug in one command cannot take the session down.

### The Open5e client

- Typed structs for monsters, weapons and spells; the parts of the payload the
  game does not use are ignored.
- Pagination follows the `next` and `previous` URLs the API returns, after
  checking they point at the same host, because those URLs come from a
  response body and are not followed blindly.
- Responses are cached in memory by URL in a `map` guarded by a
  `sync.Mutex`, with a five-minute TTL. A **reaper goroutine** wakes on a
  `time.Ticker` every minute and purges expired entries; an entry that expires
  between two ticks is dropped on read, so a stale body is never served.
  `cache` prints hits, misses, purges and the TTL.
- Two quirks of the real API are handled: `actions` is sometimes `""` instead
  of an array, and `speed` mixes numbers with booleans (`"hover": true`). A
  `200` response with no name is treated as a miss rather than becoming an
  empty stat block.

### Combat

- Attack roll: `d20 + attack bonus` against the target's armour class. A
  natural 20 always hits and doubles the damage dice; a natural 1 always
  misses.
- `explore` draws `1 + danger/2` candidate foes from the region's list,
  fetches them concurrently and keeps the worst of them.
- `flee` is one `d20 + 2 + level/2` against `10 + CR/2` (+3 against a boss).
  Failing it gives the foe a free hit.
- Victory grants XP from the challenge rating (the 5e table, CR 1/4 = 50,
  CR 2 = 450, CR 15 = 13000), a few gold, a bestiary entry with a kill count,
  and sometimes a weapon off the body.
- Defeat is not the end: you wake up at the start region with 1 HP, one tenth
  of your gold gone, and nothing written to disk.

Two house rules worth naming:

- **Rider damage.** Open5e writes a monster's whole damage output into one
  expression: the Mummy Lord's rotting fist is `3d6+6d6` because the second
  group is rot that only lands on a failed saving throw. Grimhold has no
  saving throws, so the first dice group plus any flat modifier is the damage
  on a hit, and the remaining groups become rider damage that only lands on a
  critical. Without that split every high-CR monster would be unkillable.
- **Monsters with no attack.** Some stat blocks (swarms, some casters) carry no
  rollable attack. Those get one improvised from their challenge rating, and
  the game says so when it describes them.

### Character

Level 1 starts with 30 HP, `+4` to hit, armour class 13 and a notched
shortsword. Reaching level *n* takes `(n-1)² × 100` XP: 100, 400, 900, 1600.
Each level adds 10 max HP, `+1` to hit, heals you fully, and armour class
creeps up by 1 every two levels. Damage is the weapon dice plus `2 + level`.

### Save files

`save` writes location, character, pack, spells, slain bestiary, gold, rest
state, slain bosses and any unfinished encounter to `save.json`.

The write is atomic: the bytes go to a temporary file in the same directory and
are renamed over the real save only once they are on disk. A crash, a quit or
a death part way through leaves the previous save intact. Nothing is ever
written automatically, so dying mid-`explore` cannot touch the file.

`load` decodes and validates the whole file before touching any game state: a
corrupt, hand-edited or foreign save is refused and the running session is left
exactly as it was. A missing file is a message, not a crash.

## Bonus features

All seven are implemented. The assignment caps the bonus at **+12**.

| Bonus | Pts | Where it lives | Notes |
|---|---|---|---|
| **Ambush ticker** | +6 | `internal/repl/repl.go` | Input is read on its own goroutine and handed over on a channel, so `Run` can `select` between a typed line and a `time.Ticker`. An ambush therefore interrupts the prompt instead of waiting for you to press return. Only fires outside a fight, in a region with danger 1 or more. `-ambush 0` turns it off. |
| **Test suite** | +6 | `*_test.go` | 132 test functions, no network: seeded combat, cache expiry with an injected clock, API decoding against `httptest.Server`. See below. |
| **Loot & equipment** | +4 | `internal/game/game.go` | `weapons` lists the SRD melee weapons from `/v1/weapons/`, `loot` scavenges a region once per visit, kills sometimes drop a weapon, `equip <weapon>` swaps it in and damage follows the new dice. |
| **Spells** | +4 | `internal/game/creation.go` | Nine spells are fetched from `/v1/spells/` at creation, you pick two, `cast <spell>` once per rest. The dice are pulled out of the spell's own description; healing spells work out of combat, damage spells need a target and cost you your turn. |
| **Faster dungeon** | +4 | `internal/open5e/client.go` | `travel` warms the cache for the region's foes concurrently (`sync.WaitGroup` with a bounded semaphore), and `explore` fetches its candidate draws the same way, so the first encounter does not wait on the network. |
| **Boss region** | +3 | `world.json`, `internal/game/game.go` | `hall-of-the-mummy-king` is gated behind level 5 and holds a Mummy Lord (CR 15) with legendary actions. It is fought once; after that the region behaves normally. |
| **ANSI colours** | +2 | `internal/ansi` | Hand-rolled escape codes: HP bars that go green/yellow/red, danger pips, highlighted crits. Off with `-no-color`, `NO_COLOR=1` or `TERM=dumb`. |

## Tests

```bash
go test ./...
```

```bash
go test -race ./...
```

No test touches the network: the client is pointed at an `httptest.Server`
serving fixtures trimmed from real API responses, and every roll goes through a
seeded `dice.Roller`.

| Package | Coverage | What is covered |
|---|---|---|
| `internal/dice` | 94.8% | Expression parsing (table-driven), roll bounds, crit doubling, same-seed reproducibility |
| `internal/ansi` | 87.3% | HP bar clamping and colour thresholds, danger pips, stripping escapes, colour on and off |
| `internal/world` | 94.7% | Valid and twelve kinds of broken world, plus the shipped `world.json` (exits lead both ways, the gated region is reachable) |
| `internal/open5e` | 89.6% | Decoding, `actions: ""`, `hover: true`, pagination both ways, refusing a foreign `next` URL, 404 and 500, cache TTL with an injected clock, reaper, concurrent access, prefetch deduplication |
| `internal/repl` | 90.3% | Help generated from the table, aliases, every wrong argument count, recovery from a panicking command, end of input, context cancellation, a flood of nonsense, a full scripted session, the ambush interrupting the prompt |
| `internal/game` | 85.0% | Combat invariants over 10000 attack rolls, forced natural 1 and 20, rider damage, flee, death flow, XP and level-up, rider split, save/load round trip, eight kinds of broken save, atomic write, spell dice pulled out of API text |

Combat is pinned down without a fake random source: a helper searches for a
seed whose first `d20` is the wanted face, and `resolveAttack` rolls the attack
die first. `go test -race` needs cgo and a C compiler; it was not runnable on
the 32-bit Windows toolchain this was written on, so the concurrency tests were
exercised with `go test -count=3` instead.

## Sample session

Real output, replayed with `-seed 77 -ambush 0 -name Leo -no-spells` and
`NO_COLOR=1`.

```text
Grimhold - a dungeon crawler. Monster data from Open5e.
Type help for the command list, map to see where you are.

The Crossroads
Four roads meet under a leaning gallows. Someone has nailed a hand-drawn map to the post; three of the four roads have been scratched out.
Danger ----- 0 (quiet)
Exits: ashen-marsh, hollow-barrow, the-burned-mill
Things that live here: giant-rat, swarm-of-rats, bandit

grimhold [The Crossroads 30/30] > map
Known regions:
> crossroads               -----  danger 0 (quiet)
    exits: ashen-marsh, hollow-barrow, the-burned-mill
  ashen-marsh              *----  danger 1 (uneasy)
    exits: crossroads, drowned-chapel
  the-burned-mill          *----  danger 1 (uneasy)
    exits: crossroads, gravewood
  gravewood                **---  danger 2 (dangerous)
    exits: the-burned-mill, obsidian-vault
  hollow-barrow            **---  danger 2 (dangerous)
    exits: crossroads, drowned-chapel
  drowned-chapel           ***--  danger 3 (deadly)
    exits: ashen-marsh, hollow-barrow, obsidian-vault
  obsidian-vault           ****-  danger 4 (murderous)
    exits: drowned-chapel, gravewood, hall-of-the-mummy-king
  hall-of-the-mummy-king   *****  danger 5 (hopeless)    (level 5+)
    exits: obsidian-vault
Travel with: travel <region>
grimhold [The Crossroads 30/30] > travel ashen-marsh
You travel to Ashen Marsh.
Fog, iron, and things that breathe under the water. The reeds are grey with ash and they lean away from you as you pass.
Danger *---- 1 (uneasy)
grimhold [Ashen Marsh 30/30] > look
Ashen Marsh
Fog, iron, and things that breathe under the water. The reeds are grey with ash and they lean away from you as you pass.
Danger *---- 1 (uneasy)
Exits: crossroads, drowned-chapel
Things that live here: goblin, skeleton, ghoul
grimhold [Ashen Marsh 30/30] > explore
Encounter! Goblin comes out of the dark.
  Goblin  CR 1/4  AC 15  [##############] 7/7
  attacks with Scimitar at d20+4 for 1d6+2
fight to trade blows, fight all to see it through, flee to run.
grimhold [Ashen Marsh 30/30 !Goblin] > fight
-- round 1 --
  Leo misses with Notched shortsword. d20(6)+4 = 10 vs AC 15
  Goblin [##############] 7/7
  Goblin hits with Scimitar for 5 damage. d20(10)+4 = 14 vs AC 13
  Leo [###########...] 25/30
fight again, fight all, or flee.
grimhold [Ashen Marsh 25/30 !Goblin] > fight all
-- round 2 --
  Leo hits with Notched shortsword for 4 damage. d20(15)+4 = 19 vs AC 15
  Goblin [######........] 3/7
  Goblin hits with Scimitar for 3 damage. d20(19)+4 = 23 vs AC 13
  Leo [##########....] 22/30
-- round 3 --
  Leo hits with Notched shortsword for 6 damage. d20(11)+4 = 15 vs AC 15
  Goblin falls.
Goblin is dead.
You gain 50 experience (50 total).
You pick 3 gold out of the mess (13 gp carried).
grimhold [Ashen Marsh 22/30] > character
Leo, level 1
  HP          [##############......] 22/30
  XP          50 (50 to level 2)
  Attack      d20+4 vs armour class
  Damage      1d6+3 with Notched shortsword (1d6 slashing)
  Armour      13
  Gold        13 gp
grimhold [Ashen Marsh 22/30] > loot
You turn over what is left of Ashen Marsh and find 7 gold (20 gp carried).
grimhold [Ashen Marsh 22/30] > quit
You step back out into the rain. Grimhold keeps your name.
```

The library side of the game, and the cache earning its keep. The second
`inspect orc` is served from memory:

```text
grimhold [The Burned Mill 30/30] > inspect orc
Orc
Medium Humanoid (orc), chaotic evil
Armor Class  13 (hide armor)
Hit Points   15 (2d8+6)
Speed        walk 30 ft.
STR 16  DEX 12  CON 16  INT 7  WIS 11  CHA 10
Challenge    1/2
Senses       darkvision 60 ft., passive Perception 10
Languages    Common, Orc

Actions:
  Greataxe. Melee Weapon Attack: +5 to hit, reach 5 ft., one target. Hit: 9 (1d12 + 3) slashing damage.
  Javelin. Melee or Ranged Weapon Attack: +5 to hit, reach 5 ft. or range 30/120 ft., one target. Hit: 6 (1d6 + 3) piercing damage.

Special abilities:
  Aggressive. As a bonus action, the orc can move up to its speed toward a hostile creature that it can see.
grimhold [The Burned Mill 30/30] > inspect orc
Orc
Medium Humanoid (orc), chaotic evil
...
grimhold [The Burned Mill 30/30] > cache
Open5e response cache:
  entries 2   hits 1   misses 4   purged 0
  ttl 5m0s, reaped every 1m0s by a background goroutine
```

```text
grimhold [The Crossroads 30/30] > bestiary
Bestiary (3207 monsters, ordered by challenge rating):
  Awakened Shrub                 CR 0     AC 10  HP 9    plant
  Badger                         CR 0     AC 10  HP 9    beast
  Bat                            CR 0     AC 11  HP 8    beast
  ...
bestiary next
inspect <monster> for a full stat block.
```

## The world

`world.json` holds eight regions, from the quiet crossroads to the Hall of the
Mummy King. The format is the one given in the assignment, plus two optional
fields used by the boss region:

```json
{
  "id": "hall-of-the-mummy-king",
  "name": "Hall of the Mummy King",
  "description": "A hall of painted pillars, swept clean.",
  "danger": 5,
  "min_level": 5,
  "boss": "mummy-lord",
  "foes": ["mummy", "wight"],
  "exits": ["obsidian-vault"]
}
```

It is validated on load: unique ids, a start that exists, no exit to an unknown
region or to itself, no region without a name or foes, no negative danger.
Unknown fields are rejected too, so `exit` instead of `exits` is a clear error
rather than a region you can never leave.

Extending the map is just editing the file. `internal/world/world_test.go` also
checks the shipped map for one-way exits and unreachable gated regions.

## Content licence

Monster, weapon and spell data is served by [Open5e](https://open5e.com) from
the D&D 5e SRD and other openly licensed sources, each carrying its own licence
(see `document__license_url` in the API responses). The game asks for nothing
else: no API key, no account.
