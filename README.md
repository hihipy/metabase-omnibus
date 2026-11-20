# metabase-omnibus

**See the structure, not the data.**

Exports everything that makes up a Metabase into files you can hand to an AI assistant: the shape of every connected database, and every question, model, metric, and dashboard people built on top of it, with the SQL behind each one. The assistant can then help you write queries, review reports, find gaps, or spot what could be cleaned up, without ever seeing a single record.

It only reads. It never runs a query against your data and never changes anything in Metabase.

---

## What Gets Exported

**Database shape.** Database, schema, table, and column names, column types, primary keys, and foreign keys with the column they point to. The files follow the layout of [sql-x-ray](https://github.com/hihipy/sql-x-ray), trimmed to use fewer tokens.

**Questions, models, and metrics.** Name, type, database, last update, the SQL (for questions built with the visual editor, Metabase translates them to SQL without running them), the variables a question takes, which saved questions and snippets it builds on, which dashboards show it, and the names and types of the columns it returns.

**Dashboards.** Filters, tabs, every card in layout order, and which filter feeds which variable or column on each card.

**Never exported:**

- Any value stored in your data, and any query result
- Value statistics such as minimums, maximums, and most common values
- Default values for filters, since a default can be a real value such as an ID
- Who created or edited anything
- Database hostnames, usernames, and passwords
- People's personal collections, unless you ask for them with `-include-personal`

**Left out unless you ask:**

- **Descriptions and dashboard text cards** (`-include-descriptions`). These are notes someone typed, and they could say anything, so read them before you share an export.
- **Row counts** (`-row-counts`). A small count, such as a table with 12 rows, can say something about a small group of people.

**One thing to know about SQL.** Questions are exported exactly as written. If someone typed a value into a query, such as `WHERE id = 42`, that value is in the export. Check the SQL before sharing an export outside your organization.

**Limits of the database shape.** It comes from what Metabase stores, not from the database itself, so views look like ordinary tables, and there are no indexes, stored procedures, or detail about which columns can be empty. For those, run sql-x-ray against the database directly.

---

## Before You Start

You need two things:

1. **The address of your Metabase**, for example `https://metabase.example.com`.
2. **A way to sign in.** An API key is best. A Metabase admin can create one under Admin settings, then Authentication, then API keys. Pick a group that can see what you want to export. The key gets that group's access and nothing more. If you have no key, your Metabase username and password also work, unless your organization signs in through single sign-on.

Treat the API key like a password. Keep it in a password manager, and do not put it in a shared folder or a file you commit.

---

## Install

Install [Go](https://go.dev/dl/), then run this inside the repo folder:

```
go build
```

This creates `metabase-omnibus` (or `metabase-omnibus.exe` on Windows).

---

## Run

**Windows (PowerShell):**

```powershell
$env:METABASE_URL = "https://metabase.example.com"
$env:METABASE_API_KEY = "paste-your-key-here"
.\metabase-omnibus.exe
```

**macOS or Linux:**

```bash
export METABASE_URL="https://metabase.example.com"
export METABASE_API_KEY="paste-your-key-here"
./metabase-omnibus
```

It asks three things in turn: which databases, which schemas, and which collections. Everything starts checked. Move with the arrow keys, press space to check or uncheck, `a` for all, `n` for none, Enter to confirm, and `q` to skip that part. A list with only one entry is chosen for you.

To export only one part, add `schema` or `content`:

```
metabase-omnibus schema
metabase-omnibus content
```

---

## What You Get

A folder in Downloads named `metabase-omnibus-<your Metabase address>`:

```
metabase-omnibus-metabase.example.com/
├── index.md                      every file below, and what it holds
├── schema/
│   └── Sales DB/
│       ├── public.json           one file per schema
│       └── reporting.json
└── content/
    ├── Our analytics.md          one file per collection
    ├── Our analytics/
    │   └── Finance.md
    └── _snippets.md              SQL snippets the questions use
```

Start with `index.md`, then give an assistant only the files a question needs. Smaller files cost fewer tokens and leave the assistant more room to think.

---

## Options

| Option | What it does |
| --- | --- |
| `-database "Sales DB"` | Skip the database list and use these, by name, number, or pattern |
| `-schema "sales*"` | Skip the schema list and use the schemas that match |
| `-collection "*Finance*"` | Skip the collection list and use the collections whose path matches |
| `-all` | Export everything without asking |
| `-plain` | Type your choices instead of using arrow keys |
| `-out folder` | Choose where the export folder is saved |
| `-include-descriptions` | Include descriptions and dashboard text cards |
| `-include-personal` | Include people's personal collections |
| `-include-hidden` | Include tables and columns an admin hid in Metabase |
| `-include-audit` | Include Metabase's own usage-analytics database |
| `-include-instance` | Write your Metabase address into the files. Left out by default because it identifies your organization |
| `-row-counts` | Include estimated row counts |
| `-no-compile` | Skip asking Metabase for the SQL behind visual-editor questions |
| `-ca-bundle file.pem` | Trust your organization's own security certificate |

In a typed list, choose by number (`1,4,7-9`), by name, or by pattern (`sales*`). Put a minus on every entry to leave those out instead (`-*_test`).

Your sign-in details are only read from the environment, never from options, so they never land in your command history.

---

## Testing

```
go test ./...
```

The tests run the tool against a fake Metabase built from Metabase's Sample Database, so no real server or key is needed. The fake hides a marker string everywhere a real Metabase could hand back something private: value statistics, filter defaults, people's names, connection details, personal collections, and the API key itself. The tests fail if any marker shows up in any exported file, and they fail if the tool ever asks Metabase to run a query. GitHub runs the same tests on Windows, macOS, and Linux for every change.

---

## 🛠️ Tech Stack

<!-- Go badge: copy from hihipy/hihipy -->

---

## License

This project is licensed under [CC BY-NC-SA 4.0](https://creativecommons.org/licenses/by-nc-sa/4.0/).

- **Attribution.** Credit the original work.
- **NonCommercial.** No commercial use.
- **ShareAlike.** Derivatives must use the same license.
