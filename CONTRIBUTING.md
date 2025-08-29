# Contributing to yib

Thank you for considering contributing to `yib`! We welcome all forms of contribution, from bug reports and feature suggestions to code patches and documentation improvements.

This document provides a set of guidelines to help make the contribution process smooth and effective for everyone.

## How Can I Contribute?

*   **Reporting Bugs:** If you find a bug, please open an issue on our GitHub repository. Be sure to include a clear title, a detailed description of the bug, steps to reproduce it, and the version of `yib` you are using.
*   **Suggesting Enhancements:** Have an idea for a new feature or an improvement to an existing one? Open an issue and use the "Feature Request" template to outline your suggestion.
*   **Pull Requests:** If you're ready to contribute code or documentation, please follow the process outlined below.
*   **Creating Themes:** `yib` supports custom CSS themes. If you create a new theme, we'd love to see it! You can submit it via a Pull Request.

## Pull Request Process

1.  **Fork the Repository:** Start by forking the `ericsong1911/yib` repository.
2.  **Create a Branch:** Create a new branch from `main` for your feature or bug fix. Please use a descriptive name (e.g., `feat/blah-blah-blah` or `fix/blah-blah-blah`).
3.  **Set Up Your Development Environment:**
    *   Ensure you have the Go toolchain (version 1.21 or later) installed.
    *   The `README.md` contains instructions for building from source. The most important step is running the `build.sh` script, which bundles JavaScript and compiles the Go binary.
    *   **Important:** This project uses SQLite with the FTS5 extension. You will need to build with the `fts5` tag enabled. The included `build.sh` script and `.vscode/settings.json` are already configured for this.
        ```bash
        go build -tags fts5 -o yib .
        ```
4.  **Make Your Changes:** Write your code and/or documentation.
5.  **Follow the Style Guides:**
    *   **Go:** All Go code must be formatted with `gofmt`. Please ensure your code is lint-free and follows standard Go conventions.
    *   **JavaScript:** `yib` uses clean, modern vanilla JavaScript. Please keep contributions framework-free.
    *   **Commit Messages:** Please write clear and concise commit messages. We follow the [Conventional Commits](https://www.conventionalcommits.org/) specification. For example:
        *   `feat: Add auto-refresh toggle to board view`
        *   `fix: Correct redirect logic for moderator post deletion`
        *   `docs: Update configuration variables in README`
6.  **Run Tests:** Make sure the test suite passes before submitting your pull request.
    ```bash
    go test -tags fts5 ./...
    ```
7.  **Submit the Pull Request:** Push your branch to your fork and open a pull request against the `main` branch of the original repository. Please provide a clear description of the changes you've made and link to any relevant issues.

## Code of Conduct

This project and everyone participating in it is governed by our [Code of Conduct](CODE_OF_CONDUCT.md). By participating, you are expected to uphold this code. Please report unacceptable behavior.

Thank you again for your interest in contributing to `yib`!