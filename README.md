# FreeScout Notifier

[![Go Version](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go)](https://golang.org)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go Report Card](https://goreportcard.com/badge/github.com/voicetel/freescout-notifier)](https://goreportcard.com/report/github.com/voicetel/freescout-notifier)
[![Release](https://img.shields.io/github/release/voicetel/freescout-notifier.svg)](https://github.com/voicetel/freescout-notifier/releases)

A robust Go application that monitors [FreeScout](https://freescout.net/) tickets and sends intelligent Slack notifications when tickets need attention. Built with business hours awareness, cooldown protection, and comprehensive logging.

## 🚀 Features

### Smart Notifications
- **Open Tickets**: Notifies when tickets haven't received agent responses within configurable thresholds
- **Pending Tickets**: Alerts when tickets are waiting for customer responses too long
- **Cooldown Protection**: Prevents notification spam with configurable cooldown periods
- **Rate Limiting**: Controls notification bursts to avoid overwhelming channels

### Business Hours Intelligence
- **Working Hours**: Only sends notifications during configured business hours
- **Holiday Support**: Respects company holidays loaded from JSON configuration
- **Queue Management**: Automatically queues notifications outside business hours and sends them when work starts
- **Timezone Support**: Configurable timezone handling for global teams

### Production Ready
- **Structured Logging**: JSON and text output formats with configurable verbosity
- **Database Management**: Automatic cleanup of old records and SQLite optimization
- **Connection Testing**: Built-in health checks for FreeScout and Slack
- **Statistics**: Comprehensive metrics and reporting
- **Docker Support**: Production-ready containerization
- **Systemd Integration**: Service files for automated scheduling

## 📋 Requirements

- **Go 1.21+** for building from source
- **FreeScout Instance** with MySQL database access
- **Slack Workspace** with incoming webhook configured
- **Linux/macOS** for production deployment (Windows support available)

## 🔧 Installation

### Option 1: Download Binary (Recommended)

```bash
# Download latest release
curl -L https://github.com/voicetel/freescout-notifier/releases/latest/download/freescout-notifier-linux-amd64 -o freescout-notifier
chmod +x freescout-notifier
```

### Option 2: Build from Source

```bash
# Clone repository
git clone https://github.com/voicetel/freescout-notifier.git
cd freescout-notifier

# Build binary
go build -o freescout-notifier .

# Or use make
make build
```

### Option 3: Docker

```bash
# Pull image
docker pull ghcr.io/voicetel/freescout-notifier:latest

# Or build locally
docker build -t freescout-notifier .
```

### Option 4: Automated Installation (Linux)

```bash
# Download and run installation script
curl -sSL https://raw.githubusercontent.com/voicetel/freescout-notifier/main/install.sh | sudo bash
```

## ⚙️ Configuration

### Database Connection (DSN)

The application uses a MySQL DSN (Data Source Name) for database connection:

```bash
--freescout-dsn "user:password@tcp(host:port)/database?parseTime=true&timeout=30s"
```

**DSN Format Examples:**
```bash
# Local MySQL
"freescout_user:mypassword@tcp(localhost:3306)/freescout?parseTime=true"

# Remote MySQL with SSL
"user:pass@tcp(db.example.com:3306)/freescout?tls=true&parseTime=true"

# MySQL with custom timezone
"user:pass@tcp(localhost:3306)/freescout?parseTime=true&loc=America%2FChicago"
```

### Command Line Flags

#### Database & FreeScout
```bash
--freescout-dsn string     Database DSN (default: "user:password@tcp(localhost:3306)/freescout?parseTime=true&timeout=30s")
--freescout-url string     FreeScout base URL for ticket links (required)
--db-path string          SQLite database path (default: "./notifications.db")
```

#### Slack Integration
```bash
--slack-webhook string     Slack webhook URL (required)
--slack-timeout duration  Request timeout (default: 10s)
--slack-retry-attempts int Retry attempts (default: 3)
```

#### Notification Rules
```bash
--open-threshold duration     Time before notifying about open tickets (default: 2h)
--pending-threshold duration  Time before notifying about pending tickets (default: 24h)
--cooldown-period duration    Cooldown between notifications (default: 4h)
--max-notifications-per-run int Maximum notifications per run (default: 50)
```

#### Business Hours
```bash
--business-hours-enabled       Enable business hours (default: true)
--business-hours-start int     Start hour 0-23 (default: 9)
--business-hours-end int       End hour 0-23 (default: 17)
--business-hours-timezone string Timezone (default: "America/Chicago")
--business-hours-days string   Work days "1,2,3,4,5" (default: Mon-Fri)
--holidays-file string         Path to holidays JSON file
```

#### Operational
```bash
--config-file string      JSON configuration file path
--dry-run                 Check tickets but don't send notifications
--verbose                 Enable verbose logging
--log-format string       "text" or "json" (default: "text")
--stats                   Print statistics
--cleanup                 Clean old records and exit
--retention-days int      Days to retain history (default: 90)
```

### Configuration File

Create a JSON configuration file for easier management:

```json
{
  "freescout": {
    "dsn": "freescout_user:password@tcp(localhost:3306)/freescout?parseTime=true",
    "url": "https://support.yourcompany.com"
  },
  "slack": {
    "webhook_url": "https://hooks.slack.com/services/YOUR/SLACK/WEBHOOK",
    "timeout": "10s",
    "retry_attempts": 3
  },
  "open_threshold": "2h",
  "pending_threshold": "24h",
  "cooldown_period": "4h",
  "max_notifications": 50,
  "business_hours": {
    "enabled": true,
    "start_hour": 9,
    "end_hour": 17,
    "timezone": "America/Chicago",
    "work_days": [1, 2, 3, 4, 5],
    "notify_on_open": true,
    "holidays_file": "/etc/freescout-notifier/holidays.json"
  },
  "verbose": true,
  "log_format": "json",
  "stats": true
}
```

### Holidays Configuration

Create a holidays.json file:

```json
{
  "holidays": [
    "2024-01-01",
    "2024-07-04",
    "2024-12-25",
    "2025-01-01"
  ]
}
```

## 🚀 Usage

### Quick Start

1. **Initialize the database:**
```bash
./freescout-notifier --init-db --config-file config.json
```

2. **Test connections:**
```bash
./freescout-notifier --check-connections \
  --freescout-dsn "user:pass@tcp(localhost:3306)/freescout?parseTime=true" \
  --freescout-url "https://support.company.com" \
  --slack-webhook "https://hooks.slack.com/services/..."
```

3. **Run a dry-run test:**
```bash
./freescout-notifier --dry-run --verbose --config-file config.json
```

4. **Run normally:**
```bash
./freescout-notifier --config-file config.json --stats
```

### Production Deployment

#### Option 1: Systemd (Recommended)

```bash
# Install using the automated script
sudo ./install.sh

# Configure
sudo cp config.example.json /etc/freescout-notifier/config.json
sudo editor /etc/freescout-notifier/config.json

# Initialize database
sudo -u freescout-notifier /usr/local/bin/freescout-notifier \
  --init-db --config-file /etc/freescout-notifier/config.json

# Enable and start
sudo systemctl enable freescout-notifier.timer
sudo systemctl start freescout-notifier.timer

# Check status
sudo systemctl status freescout-notifier.timer
sudo journalctl -u freescout-notifier.service -f
```

#### Option 2: Cron

```bash
# Add to crontab - check every 15 minutes
*/15 * * * * /usr/local/bin/freescout-notifier --config-file /etc/freescout-notifier/config.json >/dev/null 2>&1
```

#### Option 3: Docker

```bash
# Run with config file
docker run -d \
  --name freescout-notifier \
  -v /path/to/config.json:/etc/freescout-notifier/config.json \
  -v /path/to/data:/var/lib/freescout-notifier \
  ghcr.io/voicetel/freescout-notifier:latest

# Run with environment variables
docker run -d \
  --name freescout-notifier \
  -e FREESCOUT_DSN="user:pass@tcp(host:3306)/freescout?parseTime=true" \
  -e FREESCOUT_URL="https://support.company.com" \
  -e SLACK_WEBHOOK="https://hooks.slack.com/services/..." \
  ghcr.io/voicetel/freescout-notifier:latest
```

### Maintenance Commands

```bash
# View statistics
./freescout-notifier --stats-only --config-file config.json

# Clean up old records
./freescout-notifier --cleanup --retention-days 30 --config-file config.json

# Test specific components
./freescout-notifier --check-connections --config-file config.json

# Export configuration template
./freescout-notifier --config-file config.json --save-config config-backup.json
```

## 📊 Monitoring & Logging

### Log Formats

**Text Format (Human Readable):**
```
2024-01-15 10:30:00 INF Starting FreeScout Notifier business_hours_enabled=true dry_run=false
2024-01-15 10:30:01 INF Sent notification for ticket #1234
```

**JSON Format (Machine Readable):**
```json
{"time":"2024-01-15T10:30:00Z","level":"INFO","msg":"run_completed","stats":{"tickets_checked":15,"notifications_sent":3}}
```

### Statistics Output

```bash
=== FreeScout Notifier Statistics ===

Total Notifications: 1,247

By Status:
  sent: 1,198
  queued: 49
  pending: 0

By Type:
  open_no_agent_response: 856
  pending_no_customer_response: 391

Sent in Last 24 Hours: 23
Current Queue Size: 5

Business Hours Bursts (Last 7 Days):
  Events: 12
  Notifications Sent: 67

Response Times (Last 7 Days):
  Average: 127.3 minutes
  Minimum: 45.0 minutes
  Maximum: 480.0 minutes
```

## 🔧 Development

### Prerequisites

```bash
# Install Go 1.21+
# Install golangci-lint for linting
# Install make for build automation
```

### Setup

```bash
# Clone and setup
git clone https://github.com/voicetel/freescout-notifier.git
cd freescout-notifier

# Install dependencies
go mod download

# Run tests
go test ./...

# Lint code
golangci-lint run

# Build
make build

# Run locally
./freescout-notifier --check-connections --dry-run
```

### Project Structure

```
freescout-notifier/
├── internal/
│   ├── config/            # Configuration management
│   ├── database/          # Database connections and queries
│   ├── logging/           # Structured logging
│   ├── models/            # Data models
│   ├── notifier/          # Core business logic
│   └── slack/             # Slack client
├── deployments/           # Docker and systemd files
├── docs/                  # Documentation
├── scripts/               # Build and deployment scripts
└── tests/                 # Integration tests
```

### Testing

```bash
# Unit tests
go test ./...

# Integration tests (requires test database)
go test -tags=integration ./...

# Benchmarks
go test -bench=. ./...

# Coverage
go test -cover ./...
```

## 🤝 Contributing

1. **Fork the repository**
2. **Create a feature branch**: `git checkout -b feature/amazing-feature`
3. **Make your changes and add tests**
4. **Run linting**: `golangci-lint run`
5. **Run tests**: `go test ./...`
6. **Commit your changes**: `git commit -m 'Add amazing feature'`
7. **Push to branch**: `git push origin feature/amazing-feature`
8. **Open a Pull Request**

### Code Style

- Follow [Go Code Review Comments](https://github.com/golang/go/wiki/CodeReviewComments)
- Use `gofmt` for formatting
- Add tests for new functionality
- Update documentation for user-facing changes

## 📝 License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## 🆘 Support

- **Issues**: [GitHub Issues](https://github.com/voicetel/freescout-notifier/issues)

## 🎯 Roadmap

- [ ] **Multi-channel support** (Teams, Discord, Email)
- [ ] **Web dashboard** for configuration and monitoring
- [ ] **Ticket assignment suggestions** based on workload
- [ ] **SLA tracking** and breach notifications
- [ ] **Custom notification templates**
- [ ] **Prometheus metrics** export
- [ ] **High availability** deployment options

## 🙏 Acknowledgments

- [FreeScout](https://freescout.net/) team for the excellent help desk software
- [Go](https://golang.org/) community for the amazing language and ecosystem
- All contributors who help improve this project

---

**Made with ❤️ for better customer support**
