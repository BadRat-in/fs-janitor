# FS Janitor

> A modern filesystem lifecycle manager for macOS.
>
> Keep your filesystem clean automatically with scheduled expirations, directory watchers, and configurable cleanup policies.

![License](https://img.shields.io/badge/license-MIT-blue.svg)
![Platform](https://img.shields.io/badge/platform-macOS-black.svg)
![Language](https://img.shields.io/badge/language-Go-00ADD8.svg)

## Why?

Most computers slowly become cluttered over time.

* Downloads folders filled with installers from years ago.
* Temporary ZIP files that are never opened again.
* Screenshots scattered across the Desktop.
* Build artifacts taking up gigabytes.
* Google Drive temporary folders that should disappear after a few days.

Instead of manually cleaning them every few months, **FS Janitor** automates the entire lifecycle.

---

## Features

### One-time Expiration

Schedule a file or folder to be removed after a specific duration.

```bash
fs-janitor expire ~/Downloads/archive.zip 30d
```

Supports:

* From now
* Birth time
* Modification time

Actions:

* Move to Trash
* Permanent deletion
* (Future) Archive
* (Future) Custom actions

---

### Directory Watchers

Continuously keep directories clean.

Example:

```bash
fs-janitor watch ~/Downloads \
    --after 30d \
    --from modified \
    --trash
```

Every cleanup run, FS Janitor automatically removes files matching the configured policy.

Perfect for:

* Downloads
* Desktop
* Temporary folders
* Build directories
* Cache directories
* Google Drive
* External drives

---

### Interactive Terminal UI

Manage everything from a modern Bubble Tea interface.

* View active jobs
* Create watchers
* Schedule expirations
* Edit rules
* View cleanup history
* Monitor reclaimed storage

---

### CLI

Everything available in the TUI is also accessible from the command line.

```bash
fs-janitor watch
fs-janitor expire
fs-janitor list
fs-janitor remove
fs-janitor run
fs-janitor doctor
```

---

### Safe by Default

FS Janitor prioritizes safety.

By default it moves files to the macOS Trash instead of permanently deleting them.

Permanent deletion must be explicitly enabled.

---

### Native macOS

Designed specifically for macOS.

* LaunchAgent integration
* Finder-compatible
* Google Drive compatible
* iCloud Drive compatible
* APFS birth time support

---

## Example Workflows

### Keep Downloads Clean

```text
Downloads
└── Delete files after 30 days
```

---

### Temporary Project Folder

```text
Delete entire folder
15 days from now
```

---

### Google Drive

```text
Move temporary folders to Trash
after 14 days
```

---

### Screenshots

```text
Delete screenshots
after 7 days
```

---

## Planned Features

* Smart cleanup rules
* Pattern matching
* Include / Exclude filters
* Size-based cleanup
* Duplicate detection
* Archive old files
* Compression before deletion
* Notifications
* Cleanup analytics
* Storage insights
* Rule templates
* Plugin system

---

# Roadmap

## v0.1 — Foundation

* [ ] Project structure
* [ ] Cobra CLI
* [ ] Bubble Tea TUI
* [ ] SQLite storage
* [ ] Duration parser
* [ ] One-time expiration jobs
* [ ] Move to Trash
* [ ] Permanent delete
* [ ] Cleanup engine
* [ ] LaunchAgent installer
* [ ] Job history
* [ ] Logging

---

## v0.2 — Directory Watchers

* [ ] Watch directories
* [ ] Cleanup policies
* [ ] Birth time support
* [ ] Modification time support
* [ ] From-now expiration
* [ ] Include patterns
* [ ] Exclude patterns
* [ ] Dry-run mode
* [ ] Recursive cleanup

---

## v0.3 — Rule Engine

* [ ] Multiple rules per directory
* [ ] File extension filters
* [ ] File size filters
* [ ] Name matching
* [ ] Ignore lists
* [ ] Rule priorities
* [ ] Rule presets

---

## v0.4 — Dashboard

* [ ] Interactive dashboard
* [ ] Storage statistics
* [ ] Cleanup reports
* [ ] Search
* [ ] Bulk operations
* [ ] Job editing
* [ ] Notifications

---

## v0.5 — Automation

* [ ] Archive action
* [ ] Compression
* [ ] Execute custom commands
* [ ] Plugin API
* [ ] Webhooks
* [ ] Scheduled reports

---

## v1.0

* [ ] Stable API
* [ ] Homebrew distribution
* [ ] Automatic updates
* [ ] Documentation
* [ ] Examples
* [ ] Performance optimizations
* [ ] Comprehensive test suite

---

## Tech Stack

* Go
* Bubble Tea
* Bubbles
* Lip Gloss
* Cobra
* SQLite

---

## License

This project is licensed under the MIT License.
