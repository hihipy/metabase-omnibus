# metabase-omnibus

**See the structure, not the data.**

Exports the shape of the databases behind a Metabase into one file you can hand to an AI assistant. The assistant can then help you write queries, find gaps, or spot what could be cleaned up, without ever seeing a single record.

The file follows the same layout as [sql-x-ray](https://github.com/hihipy/sql-x-ray), so an assistant reads both the same way. The difference is where the structure comes from: sql-x-ray runs a script against the database itself, and metabase-omnibus reads what Metabase already knows. That means it works on any database Metabase supports, and it never runs any SQL.

An export of your questions, models, and dashboards is coming next.

---

## What Gets Exported

| Exported | Never exported |
| --- | --- |
| Database, schema, table, and column names | Any value stored in your data |
| Column types | Value statistics such as minimums, maximums, and most common values |
| Primary keys, and foreign keys with the column they point to | Filter dropdown lists |
| | Metabase's guesses about what a column holds, which come partly from scanning its values |
| | Database hostnames, usernames, and passwords |

Two things are left out unless you ask for them:

- **Descriptions** (`-include-descriptions`). These are notes someone typed into Metabase, and they could say anything, so read them before you share an export.
- **Row counts** (`-row-counts`). A count is not a data value, but a small one (a table with 12 rows, for example) can still say something about a small group of people.

Because the structure comes from Metabase, some things the database knows are missing: views look like ordinary tables, and there are no indexes, no stored procedures, and no detail about which columns can be empty. If you need those, run sql-x-ray against the database directly.

---

## Before You Start

You need two things:

1. **The address of your Metabase**, for example `https://metabase.example.com`.
2. **A way to sign in.** An API key is best. A Metabase admin can create one under Admin settings, then Authentication, then API keys. Pick a group that can see the databases you want. The key gets that group's access and nothing more. If you have no key, your Metabase username and password also work, unless your organization signs in through single sign-on.

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
.\metabase-omnibus.exe schema
```

**macOS or Linux:**

```bash
export METABASE_URL="https://metabase.example.com"
export METABASE_API_KEY="paste-your-key-here"
./metabase-omnibus schema
```

It shows you a list of databases, then a list of schemas, with everything checked to start. Move with the arrow keys, press space to check or uncheck, `a` for all, `n` for none, Enter to confirm, and `q` to cancel. If a list has only one entry, it is chosen for you.

The export is saved to your Downloads folder as `metabase-schema-<your Metabase address>.json`.

---

## Options

| Option | What it does |
| --- | --- |
| `-database "Sales DB"` | Skip the database list and export these, by name, number, or pattern |
| `-schema "sales*"` | Skip the schema list and export the schemas that match |
| `-all` | Export everything without asking |
| `-plain` | Type your choices instead of using arrow keys |
| `-out file.json` | Choose where the export is saved. Use `-out -` to print it to the screen |
| `-include-hidden` | Include tables and columns an admin hid in Metabase |
| `-include-audit` | Include Metabase's own usage-analytics database |
| `-include-descriptions` | Include descriptions typed into Metabase |
| `-row-counts` | Include estimated row counts |
| `-ca-bundle file.pem` | Trust your organization's own security certificate |

In the typed list, you can choose by number (`1,4,7-9`), by name, or by pattern (`sales*`). Put a minus on every entry to leave those out instead (`-*_test`).

Your sign-in details are only read from the environment, never from options, so they never land in your command history.

---

## 🛠️ Tech Stack

<!-- Go badge: copy from hihipy/hihipy -->

---

## License

This project is licensed under [CC BY-NC-SA 4.0](https://creativecommons.org/licenses/by-nc-sa/4.0/).

- **Attribution.** Credit the original work.
- **NonCommercial.** No commercial use.
- **ShareAlike.** Derivatives must use the same license.
