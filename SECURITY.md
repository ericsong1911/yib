# Security Policy for yib

yib takes security very seriously. We appreciate the efforts of security researchers and the community to help us maintain a high standard of security. This document outlines our policy for reporting vulnerabilities.

## Supported Versions

As `yib` is currently in its beta phase, security patches will only be applied to the most recent release. We encourage all users to run the latest version of the software.

| Version | Supported          |
| ------- | ------------------ |
| latest  | :white_check_mark: |
| < latest| :x:                |

## Reporting a Vulnerability

We are committed to working with the community to verify and address any potential vulnerabilities. Please do not report security vulnerabilities through public GitHub issues.

Instead, please use one of the following private methods:

1.  **Primary Method: GitHub Security Advisories**
    You can create a private vulnerability report directly on GitHub. This is the preferred and most secure method. Please navigate to the "Security" tab of the `ericsong1911/yib` repository and click "Report a vulnerability."

2.  **Alternate Method: Private Email**
    If you are unable to use GitHub Security Advisories, you can send an email to:
    **e(dot)song(at)yale(dot)edu**

Please include the following information in your report:

*   A clear and descriptive title.
*   The version of `yib` you are using.
*   A detailed description of the vulnerability, including the steps to reproduce it.
*   Any proof-of-concept code, screenshots, or logs that can help us understand the issue.
*   The potential impact of the vulnerability.

### What to Expect

After you submit a report, we will make every effort to:

*   Acknowledge receipt of your report within **3 business days**.
*   Provide an initial assessment and plan for validation within **7 business days**.
*   Keep you informed of our progress as we work to resolve the issue.
*   Release a patch to address the vulnerability as quickly as possible.
*   Publicly credit you for your discovery (unless you prefer to remain anonymous) once the vulnerability has been patched.

### Security Philosophy

`yib` is designed with a "secure by default" philosophy. Key principles include:

*   **Strict Content Security Policy (CSP):** To mitigate the risk of cross-site scripting (XSS).
*   **CSRF Protection:** All state-changing actions are protected by CSRF tokens.
*   **Secure Hashing:** User identifiers (IPs, cookies) are salted and hashed. Board passwords use `bcrypt`.
*   **Principle of Least Privilege:** The powerful moderation panel is restricted to the local network by default.
*   **Secure File Handling:** Uploaded files are validated by MIME type, size, and dimensions.

We welcome any reports that help us uphold and improve upon these principles.