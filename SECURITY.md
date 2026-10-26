# Security

## Reporting a vulnerability

Please don't open a public issue. Report it privately through GitHub: on this repository's **Security** tab, choose **Report a vulnerability**. We'll answer within a few working days and keep you posted until it's fixed.

## What the SDK sends

The SDK sends Update Prompt only counts of calls: method, host, route or path, the consumer id you set, query parameter names (never values), the minute and the number of calls. It never sends request or response bodies, header values, query values or IP addresses.

Your API key is sent only to the Usage API, in the `Authorization` header over HTTPS. The SDK never logs it or puts it in errors.
