# How MetabaseOmnibus Uses the Metabase API

This page is for anyone who wants to check what the tool asks Metabase for, or build on the API themselves. The [README](../README.md) covers using the tool.

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
- **Rate limits are not documented.** Metabase's API guide does not say how many requests it allows. This tool sends four requests at a time by default. On a small or busy Metabase, `-workers 1` sends one at a time.
- **Some endpoints are paid features.** Everything under `/api/ee/` needs a Pro or Enterprise plan.

**What this tool will not do with it**, by design: run a query, read a result, or change anything. Every call it makes is in the table under [How it talks to Metabase](#how-it-talks-to-metabase). If you build on the API yourself, the same rule is a good default: give your key a group that can read only what the job needs.
