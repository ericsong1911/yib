# yib - Yale Image Board

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Latest Release](https://img.shields.io/github/v/release/ericsong1911/yib)](https://github.com/ericsong1911/yib/releases/latest)

A classic-style, self-hosted imageboard project built with Go. `yib` is designed to be lightweight, secure, and feature-rich without relying on bulky frameworks.

**Note: `yib` is still in beta. Issues and bugs are to be expected.**

![Screenshot of yib home](docs/SCREENSHOT1.png)

![Screenshot of yib board](docs/SCREENSHOT2.png)

---

## Features

`yib` provides a complete imageboard experience for both users and administrators.

### User Features
*   **Classic Board/Thread/Reply Structure:** Familiar and intuitive imageboard layout.
*   **Rich Media Support:** Upload JPG, PNG, GIF, WebP images, and **WebM/MP4 videos** with automatic thumbnail generation.
*   **Thread-Specific User IDs:** Unique, daily-rotating IDs per thread to track conversations while maintaining anonymity.
*   **Tripcodes:** Secure identification for anonymous users.
*   **Post Management:** Users can delete their own posts.
*   **Fast, Full-Text Search:** Powered by SQLite FTS5 for instant results.
*   **Post Previews:** Hover over a `>>` backlink to preview the post.
*   **Emoji Selector:** Built-in picker for adding emojis to posts.
*   **Customization:** Multiple color schemes and client-side thread hiding.
*   **Auto-Refresh:** Live countdown timer with configurable refresh interval.
*   **Protected Boards:** Boards can be password-protected.

### Moderation & Admin Features
A comprehensive moderation panel accessible only from the local network by default.
*   **Dashboard:** View recent posts and active reports at a glance.
*   **Advanced Banning:** 
    *   Ban by IP, Cookie Hash, or **CIDR Range**.
    *   **Auto-Ban Evasion:** Automatically bans new IPs if a banned cookie is detected (and vice-versa).
*   **Mass Deletion (Nuke):** Instantly delete all posts from a specific IP or Cookie hash.
*   **Word Filters & Spam Blocking:** Regex-based filters to automatically block, replace, or ban users based on content.
*   **Post & Thread Management:** Delete any post, lock threads, and sticky threads.
*   **Auditing:** Look up a user's entire post history by their IP or cookie hash. A persistent log records all moderator actions.
*   **Database Backups:** Create live, on-demand database backups directly from the moderation panel.
*   **Board & Category Management:** Create, edit, and delete boards and categories on the fly.
*   **Global Banner:** Post a site-wide announcement banner.

### Technical Features
*   **Object Storage (S3):** Optional support for S3-compatible storage (AWS, MinIO, Cloudflare R2) for handling uploads.
*   **Secure by Default:** Built-in CSRF protection, strict CSP headers, automatic XSS prevention, secure upload file handling, and salted IP/cookie hashing.
*   **Performance:** Automatic thumbnail generation (using `ffmpeg` for video), smart batch-querying database logic.
*   **Maintainable & Robust:** Structured JSON-formatted logging, automated test suite, database migration system.
*   **Self-Contained:** Single binary executable.
*   **Configurable:** Key settings like port, database path, and storage backend configured via environment variables.

---

## Getting Started

You can run `yib` either by downloading a pre-compiled release or by building from source.

### Prerequisites
*   **FFmpeg:** Required for video thumbnail generation. Ensure `ffmpeg` is installed and in your system PATH.

### Option 1: From a Release (Recommended)

This is the easiest way to get started.

1.  Go to the [**Releases Page**](https://github.com/ericsong1911/yib/releases/latest).
2.  Download the archive for your operating system and architecture (e.g., `yib-v1.0.0-linux-amd64.tar.gz`).
3.  Extract the archive:
    ```bash
    # For .tar.gz files
    tar -xzf yib-*.tar.gz
    
    # For .zip files, use your favorite unzip tool.
    ```
4.  Navigate into the extracted `release_package` directory and run the executable:
    ```bash
    cd release_package
    ./yib
    ```
5.  Your imageboard is now running! Open your browser to `http://localhost:8080`.

### Option 2: From Source

You will need Git and the Go toolchain (version 1.21 or later) installed.

1.  **Clone the repository:**
    ```bash
    git clone https://github.com/your-username/yib.git
    cd yib
    ```
2.  **Build the project:**
    The provided build script will format, bundle JavaScript, and compile the Go binary with all necessary build tags.
    ```bash
    chmod +x build.sh
    ./build.sh
    ```
3.  **Run the application:**
    ```bash
    ./yib
    ```
4.  Your imageboard is now running at `http://localhost:8080`.

---

## Configuration

`yib` can be configured using environment variables.

### Core Settings
| Variable        | Description                              | Default                                         |
| --------------- | ---------------------------------------- | ----------------------------------------------- |
| `YIB_PORT`      | The port for the web server to listen on.| `8080`                                          |
| `YIB_DB_PATH`   | The path to the SQLite database file.    | `./yalie.db?_journal_mode=WAL&_foreign_keys=on` |
| `YIB_BACKUP_DIR`| The directory to store database backups. | `./backups`                                     |

### Object Storage (S3)
If enabled, uploads will be stored in the configured S3 bucket.
*Note: Existing local files are not automatically migrated if you switch to S3.*

| Variable              | Description                                      | Default |
| --------------------- | ------------------------------------------------ | ------- |
| `YIB_S3_ENABLED`      | Set to `true` to enable S3 storage.              | `false` |
| `YIB_S3_ENDPOINT`     | The S3 endpoint (e.g., `s3.amazonaws.com` or `play.min.io`). | -       |
| `YIB_S3_REGION`       | The S3 region (e.g., `us-east-1`).               | `us-east-1` |
| `YIB_S3_BUCKET`       | The name of the S3 bucket.                       | -       |
| `YIB_S3_ACCESS_KEY`   | Your S3 Access Key ID.                           | -       |
| `YIB_S3_SECRET_KEY`   | Your S3 Secret Access Key.                       | -       |
| `YIB_S3_PUBLIC_URL`   | Optional custom public URL (e.g., CDN domain).   | (Auto)  |
| `YIB_S3_USE_SSL`      | Set to `false` to disable SSL (useful for local MinIO). | `true`  |

### Rate Limiter
| Variable         | Description                                                            | Default          |
| ---------------- | ---------------------------------------------------------------------- | ---------------- |
| `YIB_RATE_EVERY` | The time duration between allowed bursts of posts (e.g., "30s", "1m"). | `30s`            |
| `YIB_RATE_BURST` | The number of posts allowed in a single burst.                         | `3`              |
| `YIB_RATE_PRUNE` | How often to clean up old rate limiter entries from memory.            | `1h`             |
| `YIB_RATE_EXPIRE`| How long an inactive user is kept in the rate limiter before pruning.  | `24h`            |


**Example (S3):**
```bash
export YIB_S3_ENABLED=true
export YIB_S3_ENDPOINT=nyc3.digitaloceanspaces.com
export YIB_S3_REGION=nyc3
export YIB_S3_BUCKET=my-imageboard
export YIB_S3_ACCESS_KEY=EXAMPLEKEY
export YIB_S3_SECRET_KEY=EXAMPLESECRET
./yib
```

## License

This project is licensed under the MIT License. See the [LICENSE](LICENSE) file for details.

## Acknowledgments

*   Thanks to the developers of Go, SQLite, and all the open-source libraries used.
*   Thanks to my friends who created themes, the favicon, and helped with bugtesting.