# FreeScout Notifier

[![Go Version](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go)](https://golang.org)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go Report Card](https://goreportcard.com/badge/github.com/voicetel/freescout-notifier)](https://goreportcard.com/report/github.com/voicetel/freescout-notifier)
[![Release](https://img.shields.io/github/release/voicetel/freescout-notifier.svg)](https://github.com/voicetel/freescout-notifier/releases)

A daemon that watches your [FreeScout](https://freescout.net/) help desk and posts Slack alerts for the four things that actually matter:

| Alert | Fires when | Repeats? |
|---|---|---|
| 🆕 **New ticket** | A brand-new conversation appears in FreeScout | No — once per ticket, in near real time |
| 💬 **Customer reply** | A customer replies to an existing conversation | No — once per reply, in near real time |
| 🚨 **Unanswered active** | An active ticket has been waiting for an agent reply longer than `unanswered_threshold` (default 2h) | Yes — every `reminder_interval` (default 4h) until an agent responds |
| ⏳ **Aging pending** | A pending ticket has been waiting for the customer longer than `pending_threshold` (default 24h) | Yes — every `reminder_interval` until the state changes |

FreeScout has no outbound event bus, so "real time" is achieved by polling the
FreeScout MySQL database every `poll_interval` (default **30 seconds**) and
deduplicating events in a local SQLite state database. New tickets and customer
replies are therefore delivered within seconds of arrival, and events that
arrive while the notifier was down are still caught (within the
`lookback_window`).

## How it works

```
                ┌────────────────────────────┐
                │        FreeScout DB        │
                └────────────┬───────────────┘
        every 30s (poll)     │      every 5m (sweep)
   new conversations/replies │  unanswered active + aging pending
                ┌────────────▼───────────────┐
                │      freescout-notifier    │
                │  ┌──────────────────────┐  │
                │  │ SQLite state (dedup, │  │
                │  │ reminders, queue)    │  │
                │  └──────────────────────┘  │
                └────────────┬───────────────┘
                             │  business hours?
              ┌──────────────┴───────────────┐
              ▼ open                         ▼ closed
        Slack webhook                   queue → flush at
        (immediately)                   start of business day
```

- **At-most-once delivery for events**: each new conversation / customer reply
  is recorded in the state DB before it is sent, so a Slack outage can never
  produce duplicate alerts. Tickets that genuinely need attention are still
  picked up later by the unanswered-active sweep.
- **Reminders**: aging alerts repeat on the `reminder_interval` while the
  condition persists (each repeat is labelled `Reminder #N`). When an agent
  replies, or the ticket is closed/pended/deleted, the reminder state resets —
  a later re-occurrence starts a fresh alert sequence.
- **Business hours** (optional): outside the configured schedule, alerts are
  queued in SQLite and flushed (one per second) while business hours are open.
  Queued reminders count as delivered, so they do not pile up overnight.
- **Version compatibility**: the queries adapt automatically to older FreeScout
  installations that lack the `last_customer_reply_at` column, and respect the
  `waiting_since_as_first_unanswered_customer_message` setting.

## 📋 Requirements

- **FreeScout instance** with read-only MySQL/MariaDB access
  (`SELECT` on `conversations`, `threads`, `customers`, `users`, `mailboxes`, `information_schema`)
- **Slack workspace** with an incoming webhook
- Prebuilt binaries are pure Go (no cgo) and run on Linux, macOS and Windows

## 🚀 Quick start

```bash
# 1. Build (or download a release binary)
make build

# 2. Verify connectivity
./build/freescout-notifier --check-connections \
  --freescout-dsn "user:pass@tcp(localhost:3306)/freescout" \
  --freescout-url "https://support.company.com" \
  --slack-webhook "https://hooks.slack.com/services/..."

# 3. Test what it would do (no Slack messages sent)
./build/freescout-notifier --once --dry-run --verbose \
  --freescout-dsn "..." --freescout-url "..." 

# 4. Run the daemon
./build/freescout-notifier \
  --freescout-dsn "..." --freescout-url "..." --slack-webhook "..."
```

> **First run:** the notifier marks everything already inside the
> `lookback_window` as seen without alerting, so you will not get a flood of
> old events. From then on, every new ticket/reply alerts within one poll
> interval, and the aging sweeps alert on their thresholds.

## ⚙️ Configuration

Configuration comes from flags, a JSON file (`--config-file`), or both — the
file wins over flags.

### Core

| Flag / JSON key | Default | Description |
|---|---|---|
| `--freescout-dsn` / `freescout.dsn` | — | FreeScout MySQL DSN (`parseTime=true` is appended automatically) |
| `--freescout-url` / `freescout.url` | — | FreeScout base URL for ticket links |
| `--slack-webhook` / `slack.webhook_url` | — | Slack incoming webhook URL |
| `--db-path` / `db_path` | `./notifications.db` | SQLite state database |

### Scheduling & notification rules

| Flag / JSON key | Default | Description |
|---|---|---|
| `--poll-interval` / `poll_interval` | `30s` | How often to look for new tickets and customer replies |
| `--check-interval` / `check_interval` | `5m` | How often to run the unanswered/aging sweeps |
| `--lookback-window` / `lookback_window` | `2h` | How far back to scan for events (covers restarts/downtime) |
| `--unanswered-threshold` / `unanswered_threshold` | `2h` | Wait time before alerting on an active ticket with no agent reply |
| `--pending-threshold` / `pending_threshold` | `24h` | Age before alerting on a pending ticket |
| `--reminder-interval` / `reminder_interval` | `4h` | How often to repeat an alert while its condition persists |
| `--max-notifications-per-run` / `max_notifications` | `50` | Delivery cap per cycle |

### Business hours

| Flag / JSON key | Default | Description |
|---|---|---|
| `--business-hours-enabled` / `business_hours.enabled` | `true` | Queue alerts outside business hours |
| `--business-hours-start` / `business_hours.start_hour` | `9` | Start hour (0-23) |
| `--business-hours-end` / `business_hours.end_hour` | `17` | End hour (0-23, exclusive) |
| `--business-hours-timezone` / `business_hours.timezone` | `America/Chicago` | Timezone for the schedule |
| `--business-hours-days` / `business_hours.work_days` | `1,2,3,4,5` | Work days (1=Mon … 7=Sun) |
| `--notify-on-hours-start` / `business_hours.notify_on_open` | `true` | Flush the queue while business hours are open |
| `--holidays-file` / `business_hours.holidays_file` | — | JSON file with `"holidays": ["2026-12-25", …]` |

### Operational

| Flag / JSON key | Default | Description |
|---|---|---|
| `--once` | — | Run a single monitoring pass and exit (cron-friendly) |
| `--dry-run` / `dry_run` | `false` | Do everything except send Slack messages |
| `--verbose` / `verbose` | `false` | Debug logging |
| `--log-format` / `log_format` | `text` | `text` or `json` |
| `--check-connections` | — | Test FreeScout + Slack and exit |
| `--init-db` | — | Create the state database schema and exit |
| `--stats-only` | — | Print notification statistics and exit |
| `--cleanup` | — | Delete state older than `--retention-days` and exit |
| `--retention-days` / `retention_days` | `90` | History/state retention |
| `--auto-vacuum` / `auto_vacuum` | `true` | VACUUM the SQLite file during `--cleanup` |

A full example lives in [configs/config.example.json](configs/config.example.json).

## 🚢 Deployment

### systemd (recommended)

The notifier is a long-running daemon; install the sample unit from
[configs/freescout-notifier.service](configs/freescout-notifier.service):

```bash
sudo install -m755 build/freescout-notifier /usr/local/bin/freescout-notifier
sudo useradd --system --home /var/lib/freescout-notifier freescout-notifier || true
sudo mkdir -p /etc/freescout-notifier /var/lib/freescout-notifier
sudo cp configs/config.example.json /etc/freescout-notifier/config.json   # then edit
sudo cp configs/holidays.json /etc/freescout-notifier/holidays.json       # optional
sudo cp configs/freescout-notifier.service /etc/systemd/system/
sudo chown -R freescout-notifier: /var/lib/freescout-notifier

sudo systemctl daemon-reload
sudo systemctl enable --now freescout-notifier
sudo journalctl -u freescout-notifier -f
```

### Docker

```bash
docker build -t freescout-notifier .

docker run -d --name freescout-notifier --restart unless-stopped \
  -v /path/to/config.json:/etc/freescout-notifier/config.json:ro \
  -v /path/to/data:/var/lib/freescout-notifier \
  freescout-notifier --config-file /etc/freescout-notifier/config.json
```

### Cron (`--once`)

If you prefer not to run a daemon, `--once` performs a single pass. Use a
short interval (1 minute) if you want near-real-time new-ticket alerts:

```cron
* * * * * /usr/local/bin/freescout-notifier --once --config-file /etc/freescout-notifier/config.json >/dev/null 2>&1
```

State is persisted in SQLite between runs, so deduplication and reminders
behave exactly like in daemon mode.

### Maintenance

```bash
./freescout-notifier --stats-only --config-file config.json   # statistics
./freescout-notifier --cleanup --retention-days 30 ...        # prune old state
```

Schedule cleanup weekly (cron or a systemd timer) — the daemon applies the
retention only when invoked with `--cleanup`.

## 📊 Example alerts

```
🆕 New ticket #1042 — Cannot receive calls
Customer: Jane Doe (jane@example.com)
Mailbox: Support
Received: 0 minutes ago
View ticket: <https://support.company.com/conversation/1042|Open #1042 in FreeScout>
```

```
🚨 Ticket #1017 is waiting for an agent reply — Billing question
Customer: Acme Corp (billing@acme.com)
Assigned: Unassigned
Waiting: 6 hours 12 minutes
🔁 Reminder #2 — still unhandled
View ticket: <https://support.company.com/conversation/1017|Open #1017 in FreeScout>
```

## 🔧 Development

```bash
go build ./...        # build everything
go test ./...         # unit + store tests (pure Go, no cgo, no external DB)
go vet ./...
make build            # binary with version ldflags into build/
make run-dev          # single dry-run pass, verbose
```

### Project structure

```
freescout-notifier/
├── main.go                      # entrypoint: daemon, --once, utility modes
├── internal/
│   ├── config/                  # flags, JSON config, validation
│   ├── database/
│   │   ├── freescout.go         # MySQL queries (events + aging sweeps)
│   │   └── store.go             # SQLite state: dedup, reminders, queue, log
│   ├── logging/                 # slog setup
│   ├── models/                  # shared types
│   ├── notifier/                # loops, dispatch, business hours, formatting
│   └── slack/                   # webhook client with retries
└── configs/                     # example config, systemd unit, holidays
```

## ⬆️ Migrating from v1

v2 is a rewrite; the deployment model changed from a cron/timer one-shot to a
daemon:

- **Removed**: the systemd timer (`freescout-notifier.timer`) — the service now
  runs continuously (`Type=simple`).
- **Renamed flags/config**: `--open-threshold` → `--unanswered-threshold`,
  `--cooldown-period` → `--reminder-interval`. Removed: `--stats`,
  `--db-timeout`, `--freescout-timeout`, `--slack-timeout` stays in the config
  file only.
- **New**: `--poll-interval`, `--check-interval`, `--lookback-window`,
  `--once`, and the new-ticket / customer-reply alerts.
- **State DB**: v2 uses a new SQLite schema and creates it automatically. The
  old `notifications.db` file can be deleted; v1 tables are ignored.
- **SQLite driver**: switched from `mattn/go-sqlite3` (cgo) to
  `modernc.org/sqlite` (pure Go), so `CGO_ENABLED=0` cross-compilation works.

## 📝 License

MIT — see [LICENSE](LICENSE).

## 🙌 Contributors

We welcome, acknowlege, and appreciate contributors. Thanks to these awesome people for making this project possible:

[Michael Mavroudis](https://github.com/mavroudis)

## 💖 Sponsors

| Sponsor | Contribution |
|---------|--------------|
| [VoiceTel Communications](http://www.voicetel.com) | Everything :) |

---

**Made with ❤️ for better customer support**
