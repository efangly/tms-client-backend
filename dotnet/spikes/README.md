# Phase 0 spike results (2026-10-22)

Run: `dotnet run --project spikes/Phase0Spike` (DB/timezone) or
`dotnet run --project spikes/Phase0Spike -- serial COM4 [baud] [bootDelayMs]`.

## tis620 / Thai text — PASS
- Server: MariaDB 12.3.2, `character_set_database = tis620`.
- Columns are mixed: `temp_log.*` text columns are `tis620`, `master_machine`,
  `temp_error`, `temp_log_archive`, `archive_manifest` are `utf8mb4`.
- MySqlConnector **ignores `CharacterSet=`** and always talks `utf8mb4`
  (`character_set_client/connection/results = utf8mb4`). The server converts
  to/from each column's charset, so Thai round-trips correctly: writing
  `ตู้เย็นห้องยา 1` into a `tis620` column stores TIS-620 bytes
  (`B5D9E9E0...`) and reads back identical.
- Decision: **do not** send `SET NAMES tis620` (Go does). Keep the
  connection utf8mb4; `DB_CHARSET` is accepted but only logged.
- No non-ASCII `machine_name` rows exist on the dev DB today.

## datetime / timezone — PASS
- `temp_log.insert_time`, `temp_log.send_time`, `temp_error.error_time`,
  `temp_log_archive.insert_time` are `DATETIME` (precision 0); MariaDB
  truncates the `.fff` Go sends. Others (`*_send_time`, `archived_at`) are
  `DATETIME(3)`.
- Sending a `DateTime` parameter or a `yyyy-MM-dd HH:mm:ss.fff` string gives
  identical stored values. Use `DateTime` params (Kind=Unspecified, Thai local).
- Reads come back as `DateTime` Kind=Unspecified — same as Go `loc=Local`.
- Server `time_zone=SYSTEM` (+07). On Windows both `SE Asia Standard Time` and
  `Asia/Bangkok` resolve (ICU) to +07:00 — use `Asia/Bangkok`, fallback +07.

## Serial DTR/RTS — PASS (COM4, 9600 8N1)
- go.bug.st/serial v1.8.0 (`serial_windows.go:401`) sets `DTR_CONTROL_ENABLE`
  and `RTS_CONTROL_ENABLE` when `InitialStatusBits` is nil (our case).
- C# equivalent `SerialPort { DtrEnable = true, RtsEnable = true }` works
  against the real probe: `"A\r"` → `41415A035A198C5A0D` (9-byte frame,
  1 probe, raw 0x198C = 25.40 °C) on every attempt, terminated by 0x0D.
- Device replies take ~0.8–1.6 s, so the 3 s read timeout is enough.
- With a 0 ms boot delay the first command still got a valid reply (1.56 s),
  so a reset-on-open could not be confirmed on this unit. Keep the 2 s
  `DEFAULT_SERIAL_BOOT_DELAY` anyway to match Go and stay safe with
  FTDI-reset boards.
- COM ports are exclusive: Go and C# cannot hold COM4 at the same time, so a
  side-by-side run on one machine must leave serial probes to only one of them.
