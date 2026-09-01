# CONTEXT.md — Gym tracker

A functional and design description of the application. **Technologies, file structure and
architecture are deliberately left unspecified** — they are chosen anew at implementation
time. What follows is only what the application has to do and how it has to look.

> Note on language: the application's **interface is in Russian** (see §5, "Interface copy").
> The codebase and its documentation are in English. Where this document quotes a piece of UI
> text, the Russian original is kept alongside the translation.

---

## 1. What this is for

A personal application for recording strength workouts right there at the gym, from a phone.
It replaces a notebook and the notes app.

The user is one person who trains four times a week on a fixed program. During a workout they
need to see what comes next, remember what weight they used last time, and mark a set as done
in one second between sets. Afterwards, they need to see whether the weights are going up.

**The main scenario:** phone in hand, 90 seconds between sets, hands chalked or sweaty, screen
may have gone dark. Everything has to work from a single tap and demand no attention.

## 2. Mandatory properties

These are not wishes but requirements; violating them is why previous versions were rebuilt:

1. **Instant saving.** Every change — marking a set, a weight, a rep count — is saved at the
   moment of the action, not by a button at the end. The app can be closed at any second
   without data loss.
2. **Visible save status.** The user has to understand whether something was saved. Silent
   data loss is unacceptable: if a save fails, that is shown explicitly and conspicuously.
3. **Resuming an interrupted workout.** Closed the app halfway through, came back an hour
   later or from another device — the workout is waiting in exactly the state it was left in.
4. **No system dialogs** (`alert`, `confirm`, `prompt` and the like). They do not work in
   standalone mode on iOS. Every confirmation is built from interface elements.
5. **History is inviolable.** Nothing in the interface may lead to the loss of a recorded
   workout without the user's explicit intent.

## 3. Domain model

**Program** — a list of training days. A day holds an ordered list of exercises. An exercise
is described by:

- a stable identifier (never changed and never reused — history hangs off it),
- a name,
- a scheme ("4×6–8", "3×40 m"),
- a number of sets,
- a default rep value (pre-filled when a set is marked),
- a "weighted" flag (for unweighted exercises the weight field is not shown: planks, pull-ups).

The program changes roughly every 6–8 weeks. Changing it must not require editing the
application's code and must not touch history. The program is a single source of truth: if
data about it is stored both on the server and on the client, there must be no duplication.

**Workout (session)** — one performance of a program day: a date, a reference to the day, a
set of sets, a finished flag. There can be only one unfinished workout at a time.

**Set** — belongs to a session and an exercise, has an ordinal number, a done flag, a weight
and reps. Reps are stored as text, because they come as "12", "8/leg", "30s", "40m".

**Owner.** There is one user for now, but the data model has to account for a session
belonging to a user from the very beginning — so that adding a second person (a spouse, with
their own program and their own history) does not require migrating accumulated data.

## 4. Screens and behaviour

### 4.1. Login

The application is behind a password. One screen: a heading, a password field, a button. An
error is shown as text below the button.

The login session has to last a long time — months. Being asked for a password at the gym
every time is unacceptable. At the same time, password guessing has to be rate-limited.

### 4.2. Home screen

At the top: the name, a status line ("Сила + гипертрофия · 4 дня · N тренировок записано" —
program name, day count, workouts recorded).

If there is an unfinished workout — a conspicuous card above the list of days: which day, how
many sets are already done, when it was started. Two buttons: resume and delete.

Below that, cards for the program's days. Each holds the day's number and name, a subheading
with muscle groups, the exercises listed in small text, and on the right, when this day was
last performed (or "ещё не было" — not yet). Tapping a card starts a workout.

At the bottom: export all data, and log out. A separate, conspicuous button leads to the
progress screen.

### 4.3. Workout screen

Header: an exit button on the left, on the right a "done/total sets" counter and the autosave
indicator. Below the header, the day's name and a thin progress bar for the workout.

Then a card per exercise:

- the name and, if today's weight exceeds the all-time best, a record marker;
- the set scheme in small text;
- **last time's result** with its date — for example "Прошлый раз (14 июл): 80×8 · 80×8 ·
  82.5×6". This is the key element: the user orients by it when deciding today's weight;
- a counter of completed sets in the corner;
- a row of large square buttons — one per set.

**The set button** is the primary interactive element and has to be easy to hit with a finger
without aiming. Unmarked: a neutral look with a dash. Marked: highlighted, showing the number
of reps completed.

Under each button (only for weighted exercises), a compact weight field in kg. The keyboard
has to open numeric, and comma and period have to work identically. Half a kilo is an ordinary
step on a dumbbell rack — 28.5 kg is a normal entry, not an edge case.

Marking a set records the program's reps along with it, and the keyboard then goes to that
set's weight field. The weight is the one number the tap cannot know; the reps it can, and
they are right almost every time. What is typed in the second after a tap therefore has to
land in the weight — an editor for anything else opening over the square the finger has just
hit will collect the weight instead, silently, for as long as it takes someone to look in the
database.

The exception is an exercise with no weight at all. There the reps are the whole record, so
the tap opens their editor, as before.

Correcting the reps — six instead of eight — is a deliberate act, and there are two ways to
ask for it. Holding a marked square opens an editor in its place. A quiet control below the
set row, appearing only once something in the card is marked, opens every marked set of that
exercise at once, which is how "8, 8, 6" gets recorded in one pass. The hold is the shortcut;
the control is what makes the correction findable at all, and reachable without a finger.
Either way the editor is pre-filled with nothing and shows the program's value as a hint, so
the ordinary case costs no typing and no clearing. The field takes digits and
nothing else: where an exercise is measured in something other than repetitions — "30s",
"40m", "10/leg" — the unit is a property of the exercise, the same in every set of every
workout, so it is printed beside the field and added back on save rather than typed.

Every input on this screen asks for one and the same numeric keypad, so that moving between
them never depends on the keyboard being re-presented.

Both writes — the mark and the weight — carry the whole row, and they now follow each other
within a single gesture. So the second one must be built from what storage holds at that
moment, never from what the screen was rendered with, or the weight arrives carrying a
"not done" from before the mark landed and quietly undoes it.

Tapping a marked set again unmarks it.

**Technique reference.** Every exercise card carries a quiet control that expands a guide in
place: a one-line summary of the movement, several technique cues, the usual mistakes, and a
demonstration — either a short silent clip on a loop, or the two end positions of the movement
crossfading. Which of the two an exercise gets is a matter of what exists under a free
licence; openly licensed video of gym exercises is scarce, and most exercises get frames.

Everything, the demonstration included, is served by the application itself and stored on the
device, so an opened guide needs no connection at all — it is read at the gym. Nothing is
fetched from anyone else, which is not an implementation detail but the requirement in §9.

At the bottom, a button to finish the workout. It does not "save" (everything is saved
already) — it closes the workout and moves it into history. It is disabled until at least one
set is marked.

Leaving a workout without finishing it must not happen from an accidental touch and must not
delete data: the workout stays unfinished and available to resume.

### 4.4. Progress screen

For every weighted exercise with at least two workouts, a compact chart of best working weight
by date. Caption: the exercise's name and the current best. The record point is highlighted.
At the edges, the dates of the first and last workout.

Below that, a list of recent workouts: day, date, number of sets.

If there is too little data, a clear explanation of when charts will appear — not an empty
screen.

## 5. Design

**Mood:** a tool, not a fitness app from a store. No motivation, no badges, no streaks, no
congratulations, no exclamation marks. Calm, dense, functional.

**Dark theme is mandatory and the only one.** The app is opened at the gym, often in poor
light; a bright screen hurts between sets.

**Palette** (current; it may change, but keep the character):

- background: near-black with a cool cast (`#101216`)
- cards: one shade lighter (`#181B21`), input fields lighter still (`#1F232B`)
- borders: muted (`#2A2F39`)
- primary text: near-white (`#E8EAEE`), secondary: grey-blue (`#8A93A3`)
- navigation and chart accent: muted steel blue (`#8FB4D9`)
- achievement, record, warning: amber (`#E8B44C`)
- done, success: muted green (`#6FCF8E`)
- error: muted red

Colour carries meaning rather than decorating: green = done and saved, amber = attention or a
record, red = a problem. Nothing should be green just because.

**Typography:** the device's system font (San Francisco on iPhone). Screen headings are large,
tight and uppercase. Exercise names are noticeably larger and heavier than schemes and
captions. Inside a card there has to be a distinct hierarchy: name → scheme → last result →
sets.

**Layout:** a single column, at most ~480 px wide, centred. Cards with ~16 px rounding and
generous inner padding. Air between cards. No side menus, no bottom tabs, no modals.

**Touch targets:** the set button is no smaller than 52×52 px. Everything pressed during a
workout has to be hit on the first try. The tap highlight is disabled (mobile Safari's standard
grey flash looks cheap); a smooth state change stands in for it.

**Animation:** functional and short only (a button's state change, the progress bar). No
entrances, no bounces, no confetti.

**Interface copy:** Russian, no informal address ("ты"), no exclamations, none of "отлично",
"супер", "молодец". Units are kg and metres. Dates are short ("14 июл").

## 6. Operating conditions

- The primary device is an iPhone, Safari, added to the home screen and running full-screen
  with no address bar.
- Connectivity at the gym may be poor or absent. Ideally: working offline with catch-up sync
  when the network returns; under any circumstances, losing a marked set is unacceptable.
- It also has to open on a computer — for going through the statistics.
- The application is publicly reachable at an address on the internet, so password protection
  and brute-force limiting are mandatory.

## 7. User data

- A complete export of all workouts in machine-readable form, on the user's request, from one
  button. This doubles as a backup and as material for analysing progress elsewhere.
- Regular automatic backups of the storage.
- Deleting data only by an explicit action of the user.

## 8. Future work (account for it in the architecture; do not implement yet)

- A second user with their own program and their own history.
- Changing the program without editing code.
- A rest timer between sets (different durations for compound and accessory exercises).
- A suggested-weight hint: if last time the top of the rep range was hit on every set, propose
  an increase.
- Weekly volume statistics per muscle group.
- Notes on a workout (how you felt, the state of your lower back).

## 9. What must not be in the application

- Social features, feeds, sharing achievements.
- Gamification: points, levels, streaks, rewards.
- Advertising, and analytics that send data to third parties. **Nothing third-party at all**,
  in fact: no external script, no external font, no embedded player, no request to a host
  other than this one. The demonstrations in §4.3 are freely licensed footage served from
  this application's own storage, and their authors are credited on screen, which the licence
  requires. An earlier version embedded a YouTube player as a deliberate exception to this
  rule; it was removed, and the exception with it.
- Large exercise libraries and program builders — the program is set once and changes rarely.
  The technique reference in §4.3 is not a library: it covers only the exercises in the
  user's own program, and there is no catalogue, no search and no list of "all exercises" —
  the only way in is the card of an exercise being performed.
- Mandatory web registration — users are created by hand.
