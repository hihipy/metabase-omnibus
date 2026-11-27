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

**An API key is best.** A Metabase admin creates one in Metabase's admin settings, under API keys. Metabase's guide to [API keys](https://www.metabase.com/docs/latest/people-and-groups/api-keys) walks through it. The key belongs to a group and gets exactly that group's access, so pick a group that can see what you want to export and nothing more. A group meant for analysts is usually right. Avoid the Administrators group: an admin key can change settings and permissions, and this tool only needs to read.

**Your Metabase username and password also work**, unless your organization signs in through single sign-on, in which case you need a key.

Treat the key like a password. Keep it in a password manager, and do not put it in a shared folder or anything you commit. Admins can see what each key has been used for on Metabase's [API key usage](https://www.metabase.com/docs/latest/monitor/api-key-usage) page, which is one more reason to give every person their own key.

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

It asks one question, which databases to include, then writes a folder named for your Metabase and the time, such as `metabase-example-20261120-1925`, into your **Downloads** folder. Every run gets its own folder, so an old export is never mixed into a new one. Everything about each database you pick comes along: the shape of its tables, and every question and dashboard built on it. Every folder in it has an `index.md` listing what the folder holds and roughly how much of an AI assistant's reading capacity each file uses. Start with the one at the top.

Those two `export` or `$env:` lines last until you close the window, so later runs in the same window need only the last line.

To export only one part:

```bash
./metabase-omnibus schema     # only the shape of each database
./metabase-omnibus content    # only the questions and dashboards built on each one
```

To save it somewhere else, name the folder:

```bash
./metabase-omnibus -out ~/Desktop/metabase-export
```

---

## Picking what to include

The one question is which databases. Most people use Metabase with one database per project, team, or study, so that choice decides everything else: each database's schemas come along, and so does every collection holding questions that query it. Collections built on databases you did not pick are left out.

Nothing starts selected. Move with the arrow keys and press **x** to select a database, marked `[x]`, or **ctrl+a** to select them all. With a long list, type **/** and part of a name to narrow it, for example `PROD`. Press **enter** to confirm, or **ctrl+c** to cancel. Enter does nothing until at least one database is selected, so an empty export cannot happen by accident. The keys are always listed at the bottom of the screen. If there is only one database, it is chosen for you.

If you already know what you want, name it and skip the question. Patterns work, with `*` standing for anything:

```bash
./metabase-omnibus -database "*PROD*"
```

That takes every database with PROD in its name, which is the quick way to leave out test copies.

To narrow further inside the databases you pick, name schemas or collections the same way:

```bash
./metabase-omnibus -database "Sales DB" -schema "reporting" -collection "*Finance*"
```

To take everything without being asked, add `-all`. If the list does not draw properly in your terminal, add `-plain` to type your choice instead: numbers (`1,4,7-9`), names, patterns (`sales*`), or `*` for everything. Put a minus on every entry to take everything except those (`-*_test`).

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

One folder per database, and inside it one folder per collection, mirroring how your Metabase is organized. Every question and dashboard is its own file, and every folder has an index:

```
metabase-example-20261120-1925/
├── index.md                            every database, with counts and token totals
├── Sales DB/
│   ├── index.md                        its schemas, its collections, and every question in a table
│   ├── catalog.csv                     the same table of questions, for Excel or a script
│   ├── schema/
│   │   ├── public.json                 one file per schema
│   │   └── reporting.json
│   └── questions/
│       ├── index.md                    what sits at the top level, and the collections below
│       ├── Finance/
│       │   ├── index.md                what this collection holds
│       │   ├── D3 Sales overview.md    one file per dashboard
│       │   ├── Q42 Revenue by region.md
│       │   └── Board/
│       │       ├── index.md
│       │       └── Q57 Quarterly targets.md
│       └── _snippets.md                SQL snippets these questions use
└── Marketing DB/
    └── ...
```

A database's `index.md` lists every question and dashboard in one table, and `catalog.csv` beside it holds the same rows for Excel or a script: its ID, name, type, collection, the dashboards that show it, whether its SQL was written by hand or translated from the visual editor, when it last changed, and its size in tokens. Each name links to its file.

In `catalog.csv`, any name starting with `=`, `+`, `-`, or `@` gets a leading `'`, so Excel shows it as text instead of running it as a formula. Names come from whoever wrote them in Metabase, and a spreadsheet that runs them would be a way to attack whoever opens the file.

That makes the indexes the place to start with an assistant. Give it a database's `index.md` and it can tell you which questions to read next. To ask about one report, give it that report's file and the schema files it reads.

A schema file is compact JSON, one table per line:

```json
{"metadata":{"tool_name":"metabase-omnibus","engine":"postgres","database":"Sales DB","schema":"public", ...},
"tables":[
{"name":"orders","primary_key":{"columns":["id"]},"foreign_keys":[{"from_columns":["customer_id"],"to_schema":"public","to_table":"customers","to_columns":["id"]}],"columns":[{"name":"id","data_type":"int8"},{"name":"customer_id","data_type":"int8"},{"name":"total","data_type":"numeric"}]}
]}
```

A question file is Markdown:

````markdown
# [Q42] Revenue by region

_Definition only, no query results. SQL is shown as written, so a literal typed into a query is included._

question in Our analytics > Finance, display bar, database Sales DB, updated 2026-11-10
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
| `-out PATH` | The export folder to write; defaults to a new, timestamped folder in `~/Downloads` |
| `-database LIST` | Use these databases, by name, number, or pattern, without asking |
| `-schema LIST` | Within the chosen databases, keep only the schemas that match |
| `-collection LIST` | Within the chosen databases, keep only the collections whose path matches |
| `-all` | Take every database without asking, which is also what happens with no terminal |
| `-plain` | Type your choice instead of using the interactive list |
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

## How it talks to Metabase

Everything goes through Metabase's own API, the same one its web pages use. These are the only calls the tool makes, and every one of them reads:

| Call | What it returns | What the tool keeps |
| --- | --- | --- |
| `GET /api/database` | Every database your sign-in can see | Name, engine, and number. Never the connection details |
| `GET /api/database/:id/metadata` | Every table and column Metabase knows about. Metabase's reference says this also returns field values, which are real data | Names, types, and keys. Field values and the statistics Metabase gathered from your data are discarded on arrival and never written; the tests check this |
| `GET /api/collection` | Every collection, with where it sits in the tree | Names and paths, minus personal collections |
| `GET /api/card` and `GET /api/card/:id` | Questions, models, and metrics | Definitions and SQL. Never results, filter defaults, or who made them |
| `POST /api/dataset/native` | The SQL behind a visual-editor question | The SQL. Metabase translates the question without running it |
| `GET /api/dashboard/` and `GET /api/dashboard/:id` | Dashboards and their cards | Layout, filters, and wiring |
| `GET /api/native-query-snippet` | Shared SQL snippets | Only the snippets the exported questions use |
| `POST` and `DELETE /api/session` | A sign-in session | Only when you sign in with a password instead of a key |

To look at any of these yourself, `curl` with your key works:

```bash
curl -H "x-api-key: $METABASE_API_KEY" "$METABASE_URL/api/database"
curl -H "x-api-key: $METABASE_API_KEY" "$METABASE_URL/api/card/42?legacy-mbql=true"
```

The `legacy-mbql=true` on the second line asks newer versions of Metabase for questions in the older, documented shape, which is what this tool reads.

The full reference is Metabase's [API documentation](https://www.metabase.com/docs/latest/api). Metabase also has a gentler [introduction to its API](https://www.metabase.com/learn/administration/metabase-api). The API is not versioned, so a Metabase upgrade can change it. If this tool breaks after an upgrade, that is the first place to look.

Metabase's paid plans include [serialization](https://www.metabase.com/docs/latest/installation-and-operation/serialization), which exports the same content as YAML for moving it between servers. This tool covers the reading side on any plan.

---

## What the Metabase API can do, and where it stops

This tool uses a small, read-only corner of the API. Here is all of it, counted from Metabase's own machine-readable reference, [`docs/api.json`](https://github.com/metabase/metabase/blob/master/docs/api.json), as it stood in November 2026: **765 operations in 128 areas**. 557 of them work on every plan. The other 208, in 47 areas under `/api/ee/`, are for Metabase's paid Pro and Enterprise plans.

| What you can work with | Areas | Read | Change | Examples, from Metabase's own descriptions |
| --- | ---: | ---: | ---: | --- |
| Questions, dashboards, and collections | 14 | 63 | 58 | Create and edit questions and dashboards; move many questions into one collection at once; revert something to an earlier revision; snippets, bookmarks, and timelines |
| Running queries and sharing results | 10 | 59 | 29 | Run a query and get its results; get the SQL behind a visual-editor question; public links; embedding with signed tokens |
| Databases, tables, and fields | 14 | 52 | 63 | Add a database; edit table and column details; turn an uploaded CSV into a new table and model; caching; transforms |
| Shared definitions | 6 | 20 | 25 | Metrics, segments, measures, glossary entries, and actions |
| People, access, and sign-in | 10 | 23 | 38 | Create users; update all permissions in one batch; create API keys; sign in and out; login history |
| Alerts and subscriptions | 7 | 14 | 26 | Dashboard subscriptions, alerts, and their Slack and email channels |
| Settings and running Metabase | 14 | 22 | 15 | Read and change settings; first-time setup; task history; logging; search |
| AI features | 5 | 12 | 33 | Metabot and the AI agent endpoints |
| Other | 1 | 2 | 3 | `/api/osi`, not covered here |
| Paid plans (`/api/ee/`) | 47 | 99 | 109 | Serialization, user provisioning (SCIM), the audit app, advanced permissions, two-factor sign-in, and more |

"Read" counts `GET` calls and "Change" counts the rest, which is an approximation: some `POST` calls only read. The one this tool uses to get a question's SQL is a `POST` that changes nothing.

For one call in detail, Metabase's [API reference](https://www.metabase.com/docs/latest/api) is the friendly version of that same file, and its [introduction to the API](https://www.metabase.com/learn/administration/metabase-api) walks through a few with examples.

**Where it stops:**

- **It changes without warning.** The API has no version numbers, so there is no way to pin to a stable one. Metabase says it rarely changes or removes endpoints, and it lists breaking changes in its [API changelog](https://www.metabase.com/docs/latest/developers-guide/api-changelog).
- **A key can do what its group can do, nothing more.** Creating and listing keys needs an admin.
- **Results are capped.** By default, a query run through the API returns at most 2,000 rows of raw data, or 10,000 rows when the query groups and summarizes. An admin running Metabase on their own server can raise both limits with [environment variables](https://www.metabase.com/docs/latest/configuring-metabase/environment-variables); on Metabase Cloud, Metabase support can change them.
- **Rate limits are not documented.** Metabase's API guide does not say how many requests it allows. This tool sends one request at a time, which keeps the load on your Metabase to roughly what one person clicking through it would cause.
- **Some endpoints are paid features.** Everything under `/api/ee/` needs a Pro or Enterprise plan.

**What this tool will not do with it**, by design: run a query, read a result, or change anything. Every call it makes is in the table in the section above. If you build on the API yourself, the same rule is a good default: give your key a group that can read only what the job needs.

---

## Things it cannot do

**It sees what Metabase knows, not what the database knows.** Views look like ordinary tables, and there are no indexes, stored procedures, or detail about which columns can be empty. If you need those, run [sql-x-ray](https://github.com/hihipy/sql-x-ray) against the database directly.

**It sees only what your sign-in can see.** A collection or database your group cannot open is left out without a word, because the tool cannot know it exists.

**Visual-editor questions need permission to write SQL.** Translating them into SQL needs the same access as writing SQL by hand in that database. Without it, the file shows Metabase's own description of the question instead, which an assistant can still read.

**Single sign-on alone is not enough.** If your organization signs in only through single sign-on and you have no API key, the tool cannot sign in. Ask an admin for a key.

---

## For developers

Written in Go. The interactive list is Charm's [huh](https://github.com/charmbracelet/huh), styled with [Lip Gloss](https://github.com/charmbracelet/lipgloss); everything else is the standard library. Dependencies are pinned by version and checksum in `go.sum`.

```
metabase-omnibus/
├── main.go                  commands, flags, the database picker, writing one folder per database
└── internal/
    ├── metabase/client.go   API client: sign-in, certificates, refuses redirects and plain http
    ├── schema/schema.go     database shape, in sql-x-ray's layout
    ├── content/content.go   collections, questions, dashboards, snippets
    ├── ui/                  the huh list, the typed fallback, colour, progress bar
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
