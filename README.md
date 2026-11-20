# MetabaseOmnibus

[![Link Check](https://github.com/hihipy/metabase-omnibus/actions/workflows/links.yml/badge.svg)](https://github.com/hihipy/metabase-omnibus/actions/workflows/links.yml)
[![License: CC BY-NC-SA 4.0](https://img.shields.io/badge/License-CC%20BY--NC--SA%204.0-lightgrey.svg)](https://creativecommons.org/licenses/by-nc-sa/4.0/)

**Built with**

[![GitHub Actions](https://img.shields.io/badge/GitHub%20Actions-2088FF?style=flat&logo=githubactions&logoColor=white)](https://github.com/features/actions)
[![Go](https://img.shields.io/badge/Go-00ADD8?style=flat&logo=go&logoColor=white)](https://go.dev)
[![JSON](https://img.shields.io/badge/JSON-000000?style=flat&logo=json&logoColor=white)](https://www.json.org)
[![Markdown](https://img.shields.io/badge/Markdown-000000?style=flat&logo=markdown&logoColor=white)](https://commonmark.org)
[![Metabase](https://img.shields.io/badge/Metabase-509EE3?style=flat&logo=metabase&logoColor=white)](https://www.metabase.com)

**Everything in a Metabase, in files an AI can read, with none of the data.**

MetabaseOmnibus reads a Metabase and writes down how it is built: the shape of every database behind it, and every question, model, metric, and dashboard people made on top of it, with the SQL behind each one. Hand the result to ChatGPT or Claude and ask it to write a query, review a report, or find what could be cleaned up. It never sees a single record.

---

## What problem this solves

Say you want an AI assistant's help with your Metabase. To write a query, it needs to know your tables and columns. To review a report, it needs the SQL. To spot duplicate or abandoned questions, it needs all of them at once. Today that means clicking into question after question and copying SQL by hand, and describing your tables from memory.

Metabase's own export (serialization) covers this, but only on its paid plans, and it is built for moving content between servers rather than for reading.

There is a second problem. Much of what Metabase stores about a database comes from scanning its values: the smallest and largest number in a column, its most common entries, the options in a filter dropdown. If your data is sensitive, those must never leave. Generic export tools copy them along with everything else.

MetabaseOmnibus does both jobs. It collects the whole picture, and it leaves out everything that came from the data.

---

## Installing it

You need two things, and both are free.

### Step 1: install Go

Go is the programming language this tool is written in. Installing it gives you the command that turns the source code into a working program.

**macOS.** If you have [Homebrew](https://brew.sh), open **Terminal** and run:

```bash
brew install go
```

If you do not have Homebrew, download the macOS installer from [go.dev/dl](https://go.dev/dl/) and double-click it.

**Windows.** Download the Windows installer from [go.dev/dl](https://go.dev/dl/) and run it. Then use **PowerShell** wherever this page says Terminal.

**Linux.** Use your package manager, for example `sudo apt install golang-go`, or download from [go.dev/dl](https://go.dev/dl/).

To check it worked, run this. It should print a version number.

```bash
go version
```

### Step 2: download and build this tool

In Terminal, run these three lines one at a time:

```bash
git clone https://github.com/hihipy/metabase-omnibus.git
cd metabase-omnibus
go build
```

The first line copies this project to your computer. The second moves into the folder it created. The third builds the program, which takes a few seconds and prints nothing when it works.

You now have a file called `metabase-omnibus` (`metabase-omnibus.exe` on Windows) in that folder. That is the whole tool: one file, nothing installed anywhere else, nothing running in the background.

---

## Signing in

The tool needs two things from you: your Metabase's address, and a way to sign in.

**An API key is best.** A Metabase admin creates one in Metabase's admin settings, under API keys. The key belongs to a group and gets exactly that group's access, so pick a group that can see what you want to export and nothing more. A group meant for analysts is usually right. Avoid the Administrators group: an admin key can change settings and permissions, and this tool only needs to read.

**Your Metabase username and password also work**, unless your organization signs in through single sign-on, in which case you need a key.

Treat the key like a password. Keep it in a password manager, and do not put it in a shared folder or anything you commit.

The tool only reads the sign-in from your environment, never from what you type after the command, so it never lands in your command history.

---

## Using it

**macOS or Linux:**

```bash
export METABASE_URL="https://metabase.example.com"
export METABASE_API_KEY="paste-your-key-here"
./metabase-omnibus
```

**Windows (PowerShell):**

```powershell
$env:METABASE_URL = "https://metabase.example.com"
$env:METABASE_API_KEY = "paste-your-key-here"
.\metabase-omnibus.exe
```

It asks which databases, which schemas, and which collections to include, then writes a folder called `metabase-omnibus-metabase.example.com` into your **Downloads** folder. Open `index.md` in that folder first: it lists every file and what it holds.

Those two `export` or `$env:` lines last until you close the window, so later runs in the same window need only the last line.

To export only one part:

```bash
./metabase-omnibus schema     # only the shape of the databases
./metabase-omnibus content    # only questions, models, metrics, and dashboards
```

To save it somewhere else, name the folder:

```bash
./metabase-omnibus -out ~/Desktop/metabase-export
```

---

## Picking what to include

Choosing is the default, because a large Metabase can hold hundreds of schemas and collections, and you rarely need all of them:

```
> [x] Our analytics                    2 dashboards, 14 questions
  [x] Our analytics > Finance          1 dashboard, 6 questions
  [ ] Our analytics > Archive          0 dashboards, 212 questions
  [x] Our analytics > Finance > Board  3 dashboards, 9 questions

  3 of 4 collections selected
  up and down to move, space to toggle, a all, n none, enter to confirm, q to cancel
```

Everything starts ticked. Move with the arrow keys, press **space** to untick something, press **enter** when you are happy. Press **q** to skip that part of the export. A list with only one entry is chosen for you.

If you already know what you want, name it and skip the question. Patterns work, with `*` standing for anything:

```bash
./metabase-omnibus -database "Sales DB" -schema "reporting" -collection "*Finance*"
```

To take everything without being asked, add `-all`:

```bash
./metabase-omnibus -all
```

If the arrow keys misbehave in your terminal, add `-plain` to type your choices instead: numbers (`1,4,7-9`), names, or patterns (`sales*`). Put a minus on every entry to leave those out instead (`-*_test`).

---

## What gets exported

**The shape of each database.** Schema, table, and column names, column types, primary keys, and foreign keys with the column they point to. These files follow the layout of [sql-x-ray](https://github.com/hihipy/sql-x-ray), trimmed to use fewer tokens.

**Every question, model, and metric.** Its name, type, database, and last update; its SQL; the variables it takes; which saved questions and snippets it builds on; which dashboards show it; and the names and types of the columns it returns. For questions built with Metabase's visual editor, Metabase translates them into SQL without running them.

**Every dashboard.** Its filters, its tabs, every card in layout order, and which filter feeds which variable or column on each card.

**Never exported, whatever you choose:**

- Any value stored in your data, and any query result
- Statistics Metabase gathered by scanning values: smallest, largest, most common
- The options in filter dropdowns
- Default values for filters, since a default can be a real value such as an ID
- Who created or edited anything
- Database hostnames, usernames, and passwords
- Metabase's guesses about what a column holds, which partly come from scanning its values

**Left out unless you ask:**

- **Descriptions and dashboard text cards** (`-include-descriptions`). These are notes someone typed, and they could say anything.
- **Personal collections** (`-include-personal`). These are people's own drafts.
- **Row counts** (`-row-counts`). A small count, such as a table with 12 rows, can say something about a small group of people.
- **Your Metabase's address** (`-include-instance`). It identifies your organization and adds nothing to a query.

The tool only reads. It never runs a query against your data and never changes anything in Metabase. Its tests check both: see [For developers](#for-developers).

---

## Handing the files to an AI assistant

Three things are worth knowing before you paste these into ChatGPT or Claude.

**Give it only what the question needs.** Each schema and each collection is its own file so you can. A question about one report needs that report's collection file and the schema files for the tables it reads, not your whole Metabase. Smaller files cost less and leave the assistant more room to think.

**Read the SQL before sharing it outside your organization.** Questions are exported exactly as written. If someone typed a value into a query, such as `WHERE id = 42`, that value is in the export. The tool cannot remove it without changing the SQL, so every content file says so at the top.

**Tell the assistant how to treat it.** Something like this, pasted above the files, works well:

```text
These files describe the structure of a Metabase and the databases behind it: schemas, tables, columns, data types, keys, saved questions with their SQL, and dashboards. They contain no row-level data. Use the names in them only inside the SQL you write, and do not infer anything about individual people. If anything looks like personal data, credentials, or connection details, stop and say only that potentially sensitive content was detected, without displaying it.
```

---

## What the finished folder looks like

```
metabase-omnibus-metabase.example.com/
├── index.md                       every file below, and what it holds
├── schema/
│   └── Sales DB/
│       ├── public.json            one file per schema
│       └── reporting.json
└── content/
    ├── Our analytics.md           one file per collection
    ├── Our analytics/
    │   ├── Finance.md
    │   └── Finance/
    │       └── Board.md
    └── _snippets.md               SQL snippets the questions use
```

A schema file is compact JSON, one table per line:

```json
{"metadata":{"tool_name":"metabase-omnibus","engine":"postgres","database":"Sales DB","schema":"public", ...},
"tables":[
{"name":"orders","primary_key":{"columns":["id"]},"foreign_keys":[{"from_columns":["customer_id"],"to_schema":"public","to_table":"customers","to_columns":["id"]}],"columns":[{"name":"id","data_type":"int8"},{"name":"customer_id","data_type":"int8"},{"name":"total","data_type":"numeric"}]}
]}
```

A collection file is Markdown, with each dashboard and question under its own heading:

````markdown
## [Q42] Revenue by region

question, display bar, database Sales DB, updated 2026-11-10
Built on: [17] Order base
On dashboards: [D3] Sales overview
Variables: {{region}} string/=, filters column public.customers.region

```sql
SELECT region, SUM(total) FROM {{#17}} [[WHERE {{region}}]] GROUP BY region
```

Columns: region Text, sum Decimal
````

---

## All the options

| Option | What it does |
|---|---|
| `-url URL` | Your Metabase's address, instead of setting `METABASE_URL` |
| `-out PATH` | Where to put the export folder; defaults to `~/Downloads` |
| `-database LIST` | Use these databases, by name, number, or pattern, without asking |
| `-schema LIST` | Use the schemas that match, without asking |
| `-collection LIST` | Use the collections whose path matches, without asking |
| `-all` | Take everything without asking, which is also what happens with no terminal |
| `-plain` | Type a selection instead of using arrow keys |
| `-include-descriptions` | Keep descriptions and dashboard text cards |
| `-include-personal` | Keep people's personal collections |
| `-include-hidden` | Keep tables and columns an admin hid in Metabase |
| `-include-audit` | Keep Metabase's own usage-analytics database |
| `-include-instance` | Write your Metabase's address into the files |
| `-row-counts` | Keep estimated row counts |
| `-no-compile` | Skip asking Metabase for the SQL behind visual-editor questions |
| `-ca-bundle FILE` | Trust your organization's own security certificate |
| `-timeout DURATION` | How long to wait for each answer from Metabase, default `60s` |

Running `./metabase-omnibus -help` prints all of this in the terminal.

Headings and warnings are colour-highlighted when you run it in a terminal. Setting `NO_COLOR` turns the highlighting off.

---

## Things it cannot do

**It sees what Metabase knows, not what the database knows.** Views look like ordinary tables, and there are no indexes, stored procedures, or detail about which columns can be empty. If you need those, run [sql-x-ray](https://github.com/hihipy/sql-x-ray) against the database directly.

**It sees only what your sign-in can see.** A collection or database your group cannot open is left out without a word, because the tool cannot know it exists.

**Visual-editor questions need permission to write SQL.** Translating them into SQL needs the same access as writing SQL by hand in that database. Without it, the file shows Metabase's own description of the question instead, which an assistant can still read.

**Single sign-on alone is not enough.** If your organization signs in only through single sign-on and you have no API key, the tool cannot sign in. Ask an admin for a key.

---

## For developers

Written in Go with no dependencies outside the standard library.

```
metabase-omnibus/
├── main.go                  commands, flags, the three pickers, writing the folder
└── internal/
    ├── metabase/client.go   API client: sign-in, certificates, refuses redirects and plain http
    ├── schema/schema.go     database shape, in sql-x-ray's layout
    ├── content/content.go   collections, questions, dashboards, snippets
    ├── ui/                  picker, colour, progress bar
    │   ├── term_unix.go     raw terminal through stty
    │   └── term_windows.go  raw terminal through the Windows console
    └── fakemb/fakemb.go     a fake Metabase for the tests
```

```bash
go test ./...
```

The tests run against a fake Metabase built from Metabase's Sample Database, so no server or key is needed. The fake hides a marker string everywhere a real Metabase could hand back something private: value statistics, filter defaults, people's names, connection details, personal collections, and the key itself. A test fails if any marker shows up in any exported file, and the fake fails the run outright if the tool ever asks it to run a query. GitHub runs the same tests on Windows, macOS, and Linux for every change.

Every response from Metabase is read through a list of the fields the tool wants, so anything a future version of Metabase adds is dropped rather than passed along.

---

## License

This project is licensed under [CC BY-NC-SA 4.0](https://creativecommons.org/licenses/by-nc-sa/4.0/).

- **Attribution.** Credit the original work.
- **NonCommercial.** No commercial use.
- **ShareAlike.** Derivatives must use the same license.
