# Usage Stats Panel Deployment Notes

For the custom CLIProxyAPI build that restores `/v0/management/usage`, use the supplied panel:

- Source: `C:\Users\xwk\Downloads\management.html`
- Runtime target: the `management.html` file returned by `managementasset.FilePath(configFilePath)`.

Recommended Render config:

```yaml
remote-management:
  allow-remote: true
  secret-key: "<set through MANAGEMENT_PASSWORD or your config secret>"
  disable-control-panel: false
  disable-auto-update-panel: true
usage-statistics-enabled: true
```

Recommended Render environment:

```text
MANAGEMENT_PASSWORD=<strong password>
USAGE_STATS_FILE=/data/cliproxy/usage.json
MANAGEMENT_STATIC_PATH=/CLIProxyAPI/static/management.html
```

If using a persistent disk, mount it at `/data` and keep `USAGE_STATS_FILE` under `/data`.
