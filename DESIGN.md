# stackk — design

stackk to menedżer sesji AI (Claude Code, omp) ułożonych w round-robin stack.
Stack to nazwana, specjalna sesja tmuxa (`work`, `priv`, …), a każde jej okno to jeden task. Ty zarządzasz taskami w stackk, a tmux jest tylko silnikiem pod spodem.
Klikasz task, jego terminal wysuwa się z lewej, odpowiadasz i klikasz następny. Task wraca na stack sam, gdy agent skończy. Wszystko obsługuje się myszą.

## Architektura

```
            ┌──────────────────────────── tmux server ─────────────────────────────┐
            │                                                                      │
            │  Twoja sesja, np. "main"                sesja "stackk" (@stackk = 1) │
            │  ┌─ dowolne okno ──────────────────┐    ┌stack┐┌ 2 ┐┌ 3 ┐┌ 4 ┐       │
 Ty ───────▶│  │ ┌ drawer ──────┐ ┌ TUI ───────┐│    │info ││cl.││omp││cl.│ …     │
            │  │ │ agent albo   │ │ stack      ││    └─────┘└───┘└───┘└───┘       │
            │  │ │ zaślepka     │ │ running    ││         ▲ okna tasków (home)     │
            │  │ └──────────────┘ │ odłożone   ││─────────┘ swap-pane              │
            │  │                  └────────────┘│                                  │
            │  └────────────────────────────────┘                                  │
            └──────────────────────────────────────────────────────────────────────┘
                     ▲                                   │ hooki (synchroniczne): stackk mark …
                     │ polling 1 s                       ▼
              ~/.local/state/stackk/stackk.db (SQLite) ◀── stan + log
```

- **Stack = ukryta sesja tmuxa** z opcją `@stackk = 1`, trzymająca okna tasków (każde okno = home jednego taska).
- **TUI działa w pane’ie, w którym je odpalisz.** Obok niego jest jeden pane drawera.
- **Task należy do stacku, w którego sesji jest jego okno.** Wysunięty do drawera agent jest rozpoznawany po tagu pane’a.
- **tmux trzyma procesy, SQLite trzyma stan.** Bez daemona: stan zmieniają hooki i akcje z TUI.
- **Jedna instancja TUI na stack** steruje drawerem.

## Stacki

- **`stackk` odpala TUI w bieżącym pane’ie**, w dowolnym oknie i dowolnej sesji, i nigdzie Cię nie przełącza.
- **Domyślny stack** to ukryta sesja `stackk` (albo `$STACKK_STACK`), zakładana automatycznie przy pierwszym uruchomieniu. Nie ma kroku „utwórz stack”.
- **Kolejny stack:** `stackk <nazwa>` zakłada go tak samo automatycznie.
- **Sesja stacku to tylko magazyn:** okna tasków plus okno `stack` z informacją, gdzie szukać.
- **Drawer otwiera się w oknie, w którym odpaliłeś TUI.** Twoja sesja nie jest oznaczana jako stack.
- **Wysunięty agent** fizycznie siedzi wtedy poza stackiem, więc hooki rozpoznają go po tagu pane’a `@stackk_task`, a nie po sesji. Stack taska się przy tym nie zmienia.
- **Wyjście z TUI** (`q`) odstawia agenta, usuwa zaślepkę i zdejmuje zoom, czyli Twoje okno wraca do stanu sprzed odpalenia stackk.
- **„⇲ dodaj okna”** w nagłówku TUI pokazuje okna z agentem w innych sesjach, a klik przenosi okno do stacku. Z terminala robi to samo `stackk take`, a ręczne `move-window` do sesji stacku też działa.
- **Crash TUI:** agent, który został w oknie po zabitym TUI, wraca do stacku przy następnym `stackk`.

## Tożsamość: task ≠ rozmowa ≠ uruchomienie

| pojęcie        | co to                                   | klucz                           |
|----------------|-----------------------------------------|---------------------------------|
| **task**       | wpis w stackk, to, co widzisz na liście | `tasks.id`                      |
| **rozmowa**    | sesja agenta, którą da się wznowić      | `(agent, agent_session_id)`     |
| **uruchomienie** | konkretny proces w konkretnym pane’ie | `run_id`                        |

- **Proces startowany przez stackk** dostaje w env `STACKK_TASK_ID` i `STACKK_RUN_ID`, a jego pane dostaje opcje `@stackk_task` i `@stackk_run`. Hook czyta je z env, więc mapowanie zdarzenia na task jest jednoznaczne.
- **Zdarzenie ze starym `run_id`** (np. spóźniony `SessionEnd` po resume) trafia tylko do logu i nie zmienia stanu.
- **`/clear` albo zmiana rozmowy** (`SessionStart` z nowym `session_id` w tym samym `run_id`) aktualizuje `agent_session_id` taska.
- **Agent bez `STACKK_*` w env** (odpalony ręcznie w oknie stacku albo przeniesiony przez „dodaj okna”) dostaje task przy pierwszym hooku, z `run_id` nadanym przez stackk i zapisanym w opcji pane’a.

## Model stanu

Trzy niezależne wymiary. Stack to tylko widok wynikający z nich.

| wymiar      | wartości                                             | kto zmienia |
|-------------|------------------------------------------------------|-------------|
| `phase`     | `working` · `idle` · `waiting` · `unknown`           | hooki, heurystyka, ręczna korekta |
| `run_state` | `starting` · `live` · `exited`                       | stackk (spawn), hooki, reconcile |
| `deferred`  | 0 / 1                                                | tylko Ty    |

- `waiting` ma podtyp `waiting_kind`: `permission` (zgoda na tool) albo `question` (`AskUserQuestion`).
- `unknown` oznacza, że stackk nie ma wiarygodnego sygnału. Typowy przypadek: przerwałeś agenta Esc-em, a Claude nie wysyła wtedy `Stop`.

**Widoki:**

| sekcja       | warunek                                                                 |
|--------------|-------------------------------------------------------------------------|
| **STACK**    | `deferred = 0 AND run_state = 'live' AND phase IN ('idle','waiting','unknown')`, `ORDER BY queued_at` |
| **RUNNING**  | `deferred = 0 AND run_state IN ('starting','live') AND phase = 'working'` |
| **ODŁOŻONE** | `deferred = 1 OR run_state = 'exited'` (bez zarchiwizowanych)           |

```
 phase:     working ──Stop / StopFailure──▶ idle
               ▲  ◀──PostToolUse(ten sam id)── waiting ◀──PermissionRequest / AskUserQuestion── working
               └────────── UserPromptSubmit ────────── idle / waiting / unknown
            working ──cisza w pane’ie > 60 s, brak eventów──▶ unknown

 deferred:  0 ──odłóż──▶ 1 ──przywróć──▶ 0        (phase i proces żyją dalej niezależnie)
 run_state: starting ──SessionStart──▶ live ──SessionEnd / pane zniknął──▶ exited ──wznów──▶ starting
```

- **`queued_at`** ustawia się tylko przy **wejściu** do widoku STACK (przejście z „nie na stacku” na „na stacku”). Powtórzony event niczego nie zmienia.
- **„Pomiń”** ustawia `queued_at = now` i przesuwa task na koniec. `phase_since` zostaje, więc „czeka 12m” dalej jest prawdą.
- **„Następny ▶”** otwiera pierwszy task ze STACK **inny niż aktualnie otwarty**.
- **Odłożenie `working` i przywrócenie** zwraca task tam, gdzie wskazuje jego `phase`, czyli do RUNNING, jeśli dalej pracuje.
- **Heurystyka `unknown`:** reconcile co 5 s robi hash z `capture-pane` taska w `working`. Jeśli obraz się nie zmienia (spinner agenta stoi) przez 60 s i nie przyszedł żaden event, task dostaje `unknown`. Pokazuje się wtedy na stacku ze znakiem `⁇`.

## Baza: `~/.local/state/stackk/stackk.db`

```sql
CREATE TABLE tasks (
  id                  TEXT PRIMARY KEY,           -- "masz-room-7f3a"
  name                TEXT NOT NULL,
  cwd                 TEXT NOT NULL,
  agent               TEXT NOT NULL,              -- claude | omp
  agent_session_id    TEXT,
  session_path        TEXT,                       -- transcript / plik sesji omp, do --resume
  stack               TEXT NOT NULL,              -- id sesji tmuxa, np. "$3"
  deferred            INTEGER NOT NULL DEFAULT 0,
  phase               TEXT NOT NULL,              -- working | idle | waiting | unknown
  waiting_kind        TEXT,                       -- permission | question
  waiting_tool_use_id TEXT,
  phase_since         INTEGER NOT NULL,
  queued_at           INTEGER,                    -- kolejność na stacku
  last_msg            TEXT,                       -- 1 linia, max 200 znaków
  run_id              TEXT,
  run_state           TEXT NOT NULL,              -- starting | live | exited
  tmux_pane           TEXT,                       -- "%42"
  home_window         TEXT,                       -- okno taska w sesji stacku, do którego pane wraca
  error               TEXT,                       -- np. nieudane wznowienie
  note                TEXT,
  archived_at         INTEGER,
  created_at          INTEGER NOT NULL,
  UNIQUE (agent, agent_session_id)
);

CREATE TABLE events (
  id      INTEGER PRIMARY KEY,
  ts      INTEGER NOT NULL,
  task_id TEXT REFERENCES tasks(id),
  run_id  TEXT,
  source  TEXT NOT NULL,                          -- claude | omp | ty | reconcile
  event   TEXT NOT NULL,
  applied INTEGER NOT NULL,                       -- 0 = tylko zalogowane (np. stary run_id)
  detail  TEXT
);
```

- **Reducer:** jeden `stackk mark` to jedna krótka transakcja. Wpis w `events`, sprawdzenie `run_id` i aktualizacja `tasks` idą w tej samej transakcji.
- **Połączenie:** `journal_mode = WAL`, `busy_timeout = 1000`. Zapis, który się nie uda, trafia do `~/.local/state/stackk/errors.log`, a hook i tak kończy się kodem 0.
- **Bez transakcji w trakcie wołania tmuxa.** Tworzenie taska idzie w kolejności: zapis z `run_state = 'starting'` i nowym `run_id` → spawn w tmuxie → potwierdzenie `tmux_pane`. Reconcile sprząta starty, które utknęły.
- **Na zewnątrz:** `stackk ls --json` do skryptów i późniejszego odpalania.

## Źródła zdarzeń

Hooki są **synchroniczne**. Każdy robi jeden zapis do SQLite (kilka ms, timeout 1 s), więc kolejność zdarzeń zapewnia sam agent.
Oba agenty wołają `stackk mark <event> <agent>` z payloadem na stdin i `STACKK_*` / `TMUX_PANE` z env.

| event       | Claude Code hook                          | omp extension event        | efekt |
|-------------|-------------------------------------------|----------------------------|-------|
| `start`     | `SessionStart` (startup / resume / clear) | `session_start`, `session_switch` | `run_state = live`, aktualizacja `agent_session_id` |
| `prompt`    | `UserPromptSubmit`                        | `before_agent_start`       | `phase = working` |
| `stop`      | `Stop`                                    | `session_stop`             | `phase = idle`, `last_msg` |
| `fail`      | `StopFailure`                             | —                          | `phase = idle`, `error` |
| `wait`      | `PermissionRequest`, `PreToolUse(AskUserQuestion)` | `tool_approval_requested` | `phase = waiting`, `waiting_tool_use_id` |
| `tool_done` | `PostToolUse(*)`                          | `tool_approval_resolved`   | `waiting → working`, tylko przy zgodnym `tool_use_id` (brak id → dowolny) |
| `end`       | `SessionEnd`                              | `session_shutdown`         | `run_state = exited`, tylko dla bieżącego `run_id` |

- **Zdarzenia subagentów są ignorowane.** Payload ma wtedy pole identyfikujące subagenta; do sprawdzenia dla obu agentów.
- **`Stop` / `session_stop` może zostać zablokowany** przez inny hook, który każe agentowi pracować dalej. Wtedy następny `prompt` / tool event i tak cofnie stan do `working`, a przejściowe `idle` widać najwyżej przez chwilę.
- **Extension omp** woła `stackk` przez `execFileSync` z timeoutem i łapie wszystkie błędy. Nie używa detached `spawn` jak moshi, bo ten nie gwarantuje kolejności.
- **Istniejące hooki moshi zostają bez zmian.**

## Drawer: pane po prawej

- **Kliknięcie w task wysuwa jego prawdziwy terminal po prawej stronie okna** (62% szerokości, `$STACKK_DRAWER_WIDTH`). Dashboard zwęża się do listy po lewej, więc stack cały czas jest widoczny.
- **Mechanika:** `swap-pane` agenta z zaślepką, która na ten czas czeka w jego oknie w stacku. Okno domowe nie znika, a zamknięcie drawera robi swap z powrotem.
- **Fokus:**
  - po otwarciu klawiatura trafia do agenta;
  - **klik w dashboard zamyka drawer** (TUI traci fokus → blur, pierwsze kliknięcie po blurze = powrót fokusu);
  - klik w inny task od razu go otwiera;
  - zamyka też przycisk `close ▶`.
- **Kolejność:** operacje na drawerze idą przez kolejkę w TUI, więc „zamknij” i „otwórz inny” po jednym kliknięciu się nie przeplatają.
- **Scroll, zaznaczanie, kopiowanie, wklejanie:** to zwykły pane tmuxa, więc wszystko działa jak zawsze.
- **Popup tmuxa (`display-popup`) został odrzucony,** bo tmux połyka kliknięcia poza popupem i nie da się go zamknąć klikiem w tło.
- **„+ new”, rename, note i „⇲ add windows”** to wyśrodkowane modale w TUI.

## UI — CLI

`stackk` to TUI z obsługą myszy w oknie `stackk`. **Wszystkie kontrolki są w pane’ie TUI**, bo nad terminalem agenta nic nie narysujemy.
Kolory: **żółty** = czeka na Ciebie, **szary** = pracuje, **fiolet** = odłożone; agent **zielony** = claude, **fioletowy** = omp.

### Widok główny (drawer schowany, TUI na pełną szerokość)

```
 STACKK · work ▾   9 tasków                       [y]● stack 4[/]  ◐ run 3  [p]z odłożone 3[/]  [ + nowa ] [ następny ▶ ]
┌─ STACK ─────────────────────────────── od najdłużej ─┐ ┌─ RUNNING ─────────────────────────────┐
│                                                      │ │                                      │
│   1  susanshaw                claude  ✓   12m   …    │ │ ◐  disclaimer         claude    6m … │
│      PR gotowy. Mergować czy najpierw testy e2e?     │ │    ostatnio: Edit copy.ts · 20s      │
│                                                      │ │ ◓  cp-chat            omp       2m … │
│   2  wa-computer-use-fixes    omp     ?    4m   …    │ │    ostatnio: bash pnpm test · 1m     │
│      zgoda: bash pnpm test --filter computer-use     │ │ ◒  2.1.286            omp      38s … │
│                                                      │ │    ostatnio: bash git bisect · 5s    │
│   3  yum-3597                 claude  ✓    1m   …    │ └──────────────────────────────────────┘
│      Migracja przeszła. Robić backfill w tym PR?     │ ┌─ ODŁOŻONE ────────────────────────────┐
│                                                      │ │ z  masz-room        claude  ✓  2h  … │
│   4  godot-diff               claude  ⁇    3m   …    │ │    po review od backendu             │
│      cisza od 3m, brak Stop (przerwany?)             │ │ z  rich-panel-snooze omp        1d … │
│                                                      │ │ ○  deferred-tasks   claude      3d … │
└──────────────────────────────────────────────────────┘ └──────────────────────────────────────┘
┌─ LOG ─────────────────────────────────────────────────────────────────────────────────────────┐
│ 21:48:31  ↩  yum-3597               claude  stop                     → stack #3               │
│ 21:47:10  ?  wa-computer-use-fixes  omp     wait (permission)        → stack #2               │
│ 21:46:02  →  disclaimer             ty      prompt                   → running                │
│ 21:45:12  ⁇  godot-diff             reconcile  cisza 60 s            → stack #4               │
└───────────────────────────────────────────────────────────────────────────────────────────────┘
```

- **Znaczniki:** `✓` skończył, `?` czeka na zgodę/odpowiedź, `⁇` nie wiadomo, `○` proces zakończony.
- **RUNNING** pokazuje ostatnie znane zdarzenie, a nie bieżący postęp.
- **`…` na końcu wiersza** otwiera to samo menu co prawy klik.

### Lewy klik → terminal wysuwa się z lewej

Prawdziwy pane agenta, na żywo, można w nim od razu pisać. TUI zwęża się do sidebara po prawej i tam są wszystkie kontrolki.

```
┌────────────────────────────────────────────────────────────────────┐ susanshaw · claude
│                                                                    │ ~/Work/acme/billing
│ ⏺ PR gotowy: #812 "fix: susanshaw retry on 429".                   │ [ ◀ schowaj ] [ … ]
│   Testy unit przechodzą, e2e 2/14 czerwone (flaky?).               │ [ następny ▶ ]
│   Mergować czy najpierw poprawić testy e2e?                        │ ── STACK ─────────────
│                                                                    │▐1  susanshaw     12m▌
│ > najpierw e2e, pokaż który test flakuje▌                          │  2  wa-computer…  4m ?
│                                                                    │  3  yum-3597      1m
│                                                                    │  4  godot-diff    3m ⁇
│                                                                    │ ── RUNNING ───────────
│                                                                    │ ◐  disclaimer     6m
│                                                                    │ ◓  cp-chat        2m
│                                                                    │ ◒  2.1.286       38s
│ ▸▸ auto mode on (shift+tab to cycle)                               │ ── ODŁOŻONE ──────────
│                                                                    │ z  masz-room      ✓
└────────────────────────────────────────────────────────────────────┘ ○  deferred-tasks
```

- **Po wysłaniu promptu** task przeskakuje do RUNNING, a terminal zostaje wysunięty.
- **Klik w inny task** robi `open(B)` według protokołu: najpierw A wraca do siebie, dopiero potem wysuwa się B.
- **`[ ◀ schowaj ]`** albo klik w ten sam task robi `close()`.
- **Klik w task `○`** najpierw go wznawia, potem wysuwa.
- **Pierwszy klik w sidebar**, gdy fokus jest w terminalu agenta, od razu działa, bo tmux z `mouse on` przekazuje go do TUI.

### `[ + nowa ]` → nowa sesja

Formularz renderuje się w pane’ie TUI, a drawer się wtedy chowa.

```
┌─ nowa sesja ──────────────────────────────────────────────────────────┐
│                                                                       │
│  folder   [ ~/Work/acme/web                    ▾ ]              │  ← ostatnie foldery + wpisz ścieżkę
│  nazwa    [ masz-room                                  ]              │  ← domyślnie nazwa folderu / branch
│  agent    ( ● claude )  ( ○ omp )                                     │
│  prompt   [ dokończ migrację z YUM-3612                ]              │  ← opcjonalny
│                                                                       │
│                                              [ anuluj ]  [ utwórz ▶ ] │
└───────────────────────────────────────────────────────────────────────┘
```

**Utwórz** idzie w kolejności: wiersz `starting` → nowe okno w sesji stacku z `STACKK_*` w env → `claude "<prompt>"` / `omp "<prompt>"` → `open()`.

### Prawy klik (albo `…`) → menu

Menu pokazuje akcje dla konkretnego `task_id`, a nie dla pozycji wiersza. Dostępność akcji jest sprawdzana jeszcze raz w momencie kliknięcia.

```
  susanshaw (idle)      godot-diff (⁇)                masz-room (odłożony)    deferred-tasks (○)
 ┌───────────────────┐  ┌───────────────────────┐    ┌───────────────────┐   ┌───────────────────┐
 │ Otwórz            │  │ Otwórz                │    │ Otwórz            │   │ Wznów             │
 │ Pomiń (na koniec) │  │ Oznacz: czeka na mnie │    │ Przywróć          │   │ Notatka…          │
 │ Odłóż             │  │ Oznacz: pracuje       │    │ Notatka…          │   │ Historia          │
 │───────────────────│  │ Odłóż                 │    │ Historia          │   │───────────────────│
 │ Zmień nazwę…      │  │───────────────────────│    │───────────────────│   │ Archiwizuj        │
 │ Historia          │  │ Zatrzymaj             │    │ Zatrzymaj         │   └───────────────────┘
 │ Zatrzymaj         │  └───────────────────────┘    └───────────────────┘
 └───────────────────┘
```

- **Odłóż / Przywróć** zmieniają tylko flagę `deferred`. Proces i `phase` żyją dalej.
- **Wznów** (tylko `exited`):
  - `claude --resume <agent_session_id>` albo `omp --resume <session_path>` w `cwd`, z nowym `run_id`;
  - blokada `starting` chroni przed podwójnym kliknięciem;
  - błąd zapisuje się w `error`, a task zostaje `exited`, bez cichego tworzenia nowej rozmowy.
- **Zatrzymaj** najpierw próbuje łagodnie (`/exit` do agenta), a dopiero po timeoucie pyta o `kill-pane`. Historia zostaje.
- **Archiwizuj** (tylko `exited`) chowa task z list. Historia i transcript zostają.
- **Oznacz: …** to ręczna korekta `phase`, gdy heurystyka albo hooki się mylą.
- **„Przenieś do stacku…”** (w każdym menu) przenosi okno taska do innego stacku.
- **Zgody i odpowiedzi** są zawsze w terminalu agenta. Menu ich nie wysyła.
- **Lista nie przestawia wierszy,** dopóki trzymasz wciśnięty przycisk albo menu jest otwarte.

### Pusty stack

```
┌─ STACK ──────────────────────────────────────────────┐
│                                                      │
│              nic na Ciebie nie czeka                 │
│        najdłużej pracuje: disclaimer (6m)            │
│                                                      │
└──────────────────────────────────────────────────────┘
```

## Reconcile

Uruchamiany przy starcie TUI i potem co 5 s:

1. `tmux list-panes -a -F '#{pane_id} #{pane_dead} #{@stackk_task} #{@stackk_run}'`.
2. **tmux nie odpowiada** → baner „backend niedostępny”, niczego nie oznaczamy jako `exited`.
3. **Pane taska zniknął, jest `pane_dead` albo ma inny `@stackk_run`** → `run_state = exited` z wpisem `reconcile` w logu.
4. **Starty w `starting` bez pane’a po 30 s** → `exited` + `error`.
5. **Heurystyka ciszy** dla `working` → `unknown` (patrz Model).
6. **`recover()` drawera.**

## CLI poza TUI

```
stackk [stack]                           # picker albo TUI stacku
stackk new-stack <nazwa>
stackk take [stack]                      # przenieś bieżące okno do stacku
stackk new <folder> [--agent claude|omp] [--name n] [prompt]
stackk ls [--json]                       # stack / running / odłożone
stackk log [task]                        # zdarzenia
stackk resume <task>
stackk mark <event> <agent>              # z hooków
```

## Zakres v1

**W v1:**
- Jeden serwer tmuxa, jedna instancja TUI sterująca layoutem.
- Claude i omp przez małe adaptery hooków (synchroniczne).
- Model trzech wymiarów, tożsamość task / rozmowa / `run_id`, reducer w jednej transakcji.
- Nowa sesja, otwórz (drawer), pomiń, odłóż / przywróć, wznów, zatrzymaj, archiwizuj, ręczna korekta `phase`.
- Nazwane stacki (sesje z `@stackk`), picker, `+ nowy stack`, „dodaj okna z innych sesji”.
- Reconcile + `recover()` drawera.

**Później:**
- licznik w `status-right` i toasty,
- worktree przy nowej sesji,
- eksport odłożonych do plików,
- historia w drawerze (w v1 wystarczy `stackk log`),
- podgląd postępu narzędzi.

**Warunek odbioru v1** (scenariusze testowe przed rozbudową UI):
1. Spóźniony `stop` z poprzedniej tury nie cofa `working`, bo hooki są sync.
2. `SessionEnd` starego procesu po resume nie rusza nowego `run_id`.
3. Podwójny klik „Wznów” daje jeden proces.
4. Zamiana A → B → C → schowaj: każdy agent wraca do swojego `home`, a zaślepka do `stackk`.
5. Ubicie agenta w drawerze oraz ubicie zaślepki w `home` są poprawnie odtwarzane.
6. Restart TUI przy wysuniętym agencie → `recover()` przywraca układ.
7. Restart serwera tmuxa → wszystkie taski `exited`, a wznawianie działa.
8. Esc w trakcie pracy Claude → po 60 s task ma `⁇` na stacku.

## Decyzje

- **Język TUI:** Go + bubbletea (+ lipgloss), jedna binarka `stackk`, która obsługuje też `mark` z hooków.
- **Stack = nazwana sesja tmuxa z `@stackk = 1`.** Zwykłe sesje (`main`, `0`, `2`) zostają nietknięte.
- **Obecne okna z `main`:** tworzysz stack (np. `work`) i przez „dodaj okna z innych sesji…” przenosisz te, nad którymi dalej pracujesz. Resztę zostawiasz w `main`, aż wygasną.

## Otwarte pytania

- **Hooki a działające procesy:** czy już działające procesy Claude/omp łapią nowe hooki bez restartu? Jeśli nie, pojawią się dopiero po `--resume`. Do sprawdzenia przy instalacji.
- **Pola subagentów:** jakie pola w payloadzie Claude / omp odróżniają subagenta? Do sprawdzenia.
- **Heurystyka ciszy:** czy 60 s to dobry próg? Może per agent.
- **Worktree:** czy `+ nowa` ma od razu tworzyć git worktree/branch? (Na razie „później”).

## Implementacja (v1)

- `cmd/stackk`: jedna binarka (CLI + TUI + `mark` dla hooków), instalowana do `~/.local/bin/stackk`.
- `internal/store`: SQLite (schemat + reducer, testy jednostkowe).
- `internal/core`: `mark`, stacki, taski, reconcile, drawer.
- `internal/ui`: bubbletea + bubblezone.
- `omp/stackk.ts`: extension omp (symlink w `~/.omp/agent/extensions/`).
- Hooki Claude: jedna komenda `stackk mark claude` na wszystkich eventach. Event rozpoznawany jest z `hook_event_name`.
- Poza stackiem `mark` kończy się po jednym zapytaniu do tmuxa (~10 ms), bez otwierania bazy.
- Test end-to-end: `testdata/it.sh`. Izolowany serwer tmuxa i fałszywy agent (`testdata/fakeagent`) pokrywają scenariusze z „Warunku odbioru”.

## Pause, scheduled i nazwy okien

- **Pause (hide):** chowa sesję z każdej listy, a agent dalej działa w swoim oknie. Powrót przez `⏸ paused N` w nagłówku (modal) albo `stackk paused` / `stackk resume <id>`. Jeśli agent w międzyczasie się zakończył, powrót go wznawia.
- **Schedule** (dawniej Defer): sesja zostaje widoczna w sekcji SCHEDULED.
- **Sufiksy w nazwach okien:**
  - stackk sam dopisuje ` st` (scheduled) i ` p` (paused), a running i waiting zostają bez sufiksu;
  - działa to w obie strony: ręczna zmiana nazwy okna (dopisanie albo usunięcie sufiksu) zmienia stan taska;
  - okna `… st` / `… p` przejmowane do stacku od razu dostają odpowiedni stan.

## Okna bez agenta

- **Każde okno w stacku jest taskiem.** W oknie z kilkoma pane’ami task bierze pane z agentem, a jeśli go nie ma, aktywny pane.
- **Wyjście z agenta do powłoki** zostawia task na stacku ze znacznikiem `$`, bo czego nie umiemy sklasyfikować jako running, scheduled albo paused, to zostaje na stacku. Klik otwiera to samo okno.
- **Ponowne `claude` / `omp` w tym oknie** wraca task do stacku.
- **Zakończony (`○`, wznawiany przez `--resume`)** jest tylko task, którego okno zniknęło.

## Zaślepka

- **Gdy sesja jest otwarta w drawerze,** jej okno w stacku pokazuje zaślepkę: „<nazwa> is open in the stackk dashboard”.
- **Enter albo klik w zaślepkę** ściąga sesję z powrotem do tego okna, np. kiedy dotrzesz tam zwykłą nawigacją tmuxa.

## Wykrywanie stanu (zasady)

- **Hooki są źródłem prawdy.** Gdy task wysłał choć jeden hook, nazwa procesu w oknie jest ignorowana. Agent odpalony przez `fish -c` wygląda w tmuxie jak `fish`.
- **Bez hooków** (stare sesje) stackk patrzy na drzewo procesów pane’a (powłoka → agent), a nie tylko na proces na wierzchu.
- **Sesje z `+ new` / `stackk new`** startują przez Twoją powłokę z `exec`. Konfiguracja powłoki (PATH, mise, zmienne) działa, a w oknie widać agenta.
- **Naprawa starych wpisów:** task błędnie oznaczony jako `$` wraca do stanu wynikającego z ostatniego hooka.

## Usuwanie i szerokość

- **„Close & remove”** zamyka całe okno taska (agenta, powłokę i splity) i usuwa task ze stackk. Nie ma czego przejmować z powrotem. Z CLI: `stackk rm <id>`. Historia zostaje w logu.
- **Szerokość drawera** zmieniasz, przeciągając myszą granicę między listą a terminalem. Zostaje przy przełączaniu tasków i przy ponownym otwarciu.

## Testy

- `testdata/it.sh`: pełny e2e (UI, drawer, modale, pauza, sufiksy, zaślepka, przeciąganie).
- `testdata/lifecycle.sh`: cykl stacku w realnych warunkach. Agenci startują przez fish, bez promptu i z promptem. Po każdym przejściu test czeka dłużej niż cykl reconcile.

## Subagenci

- **Stop z `background_tasks`, w których jest `type: "subagent"`** (status running): tura się skończyła, ale subagent pracuje w tle, więc sesja zostaje w RUNNING. Wraca na stack przy następnym Stop bez subagentów. Format potwierdzony na prawdziwym Claude 2.1.286.
- **`type: "shell"` w tle** (serwer deweloperski, watcher) nie trzyma sesji w RUNNING.
- **Zdarzenia z wnętrza subagenta** (`agent_id`):
  - prośba o zgodę i pytanie wrzucają sesję na stack jako „czeka na Ciebie”;
  - użycie narzędzia liczy się jako aktywność;
  - jego własne Stop / SessionStart są ignorowane.

## Hide

- **„Hide (not a task)”** jest dla okien, które nie są pracą (notatki, edytor, np. `TODO`). Wypadają z list, nazwa okna się nie zmienia, a okno nie jest przejmowane z powrotem.
- **Powrót** przez `⏸ paused N · hidden M` → sekcja „hidden” w modalu albo `stackk unhide <id>`.

## Powiadomienia

- **Kiedy:** sesja przeszła z RUNNING na stack i po 2 minutach (`STACKK_NOTIFY_AFTER`) nadal czeka, a Ty jej nie otworzyłeś.
- **Pomijane,** gdy fokus terminala jest na oknie dashboardu albo sesja jest otwarta w drawerze.
- **Ile:** kilka sesji to jedno powiadomienie, najwyżej raz na minutę (`STACKK_NOTIFY_INTERVAL`).
- **Jak:** powiadomienie macOS przez `osascript`. Działa, dopóki dashboard jest uruchomiony.
