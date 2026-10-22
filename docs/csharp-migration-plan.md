# แผนย้าย TMS Backend จาก Go → C# (.NET) — Console App + Windows Service + Setup.exe

## Context

ระบบปัจจุบัน (`tms-backend`, Go ~4.5k LOC ไม่รวม test) เป็น tray app บน Windows ที่:
- เปิด REST API + SSE (Fiber) บนพอร์ต `PORT` (default 8080)
- Poll อุปกรณ์วัดอุณหภูมิผ่าน TCP (พอร์ต 8899, hex protocol) และ Serial/COM (persistent + auto-reconnect)
- บันทึกลง MySQL/MariaDB (charset `tis620`), ตรวจ alert ทุก 5 วินาที, schedule report, ส่ง Legacy API + MQTT
- Archive `temp_log` เก่าเป็นไฟล์ JSON รายวัน + restore กลับได้
- ตั้ง autostart ผ่าน registry (tray) — ไม่ใช่ service จริง

เป้าหมาย: เขียนใหม่เป็น C# ให้ **พฤติกรรม/API/รูปแบบข้อมูลเหมือนเดิม 100%** (frontend, DB, archive files, MQTT payload, legacy API ใช้ต่อได้ทันที) โดย:
- exe เดียว: **double-click = Console** (เห็น log, Ctrl+C หยุด) / **ติดตั้งแล้วรันเป็น Windows Service** (auto-start, auto-restart) — ตัด tray ออก
- ติดตั้งด้วย **Setup.exe (Inno Setup)**
- Data access: **Dapper + MySqlConnector**
- โค้ดอยู่ในโฟลเดอร์ใหม่ `dotnet/` ใน repo นี้ เก็บ Go ไว้เทียบผลจน cutover

## Tech mapping

| Go | C# (.NET 10 LTS) |
|---|---|
| Fiber + cors + logger | ASP.NET Core Minimal API (Kestrel), `AddCors`, request logging |
| GORM + go-sql-driver/mysql | Dapper + MySqlConnector (+ `CodePagesEncodingProvider` สำหรับ tis620) |
| godotenv | DotNetEnv (โหลด `.env` ข้างๆ exe → `IConfiguration`) |
| goroutine + ticker + stopChan | `BackgroundService` + `PeriodicTimer` + `CancellationToken` |
| channel subscribers (SSE) | `System.Threading.Channels` |
| go.bug.st/serial | `System.IO.Ports.SerialPort` |
| paho.mqtt | MQTTnet |
| utils/logger.go (error_YYYY-MM-DD.txt) | Serilog: Console + File (daily, เฉพาะ Error, retention) + EventLog เมื่อเป็น service |
| systray + registry startup | `Microsoft.Extensions.Hosting.WindowsServices` (`UseWindowsService()`) |
| `-X version.Version` | `<InformationalVersion>` ผ่าน `dotnet publish -p:Version=` |
| script/main.go | sub-command `tms-backend.exe archive-table <name>` |

## โครงสร้างโปรเจกต์

```
dotnet/
  TmsBackend.sln
  src/TmsBackend/                 # exe หลัก (Web SDK)
    Program.cs                    # host, mode detect, sub-commands
    Config/                       # AppOptions (bind จาก .env/env vars)
    Data/                         # Db (connection factory), repositories (Dapper)
    Models/                       # MasterMachine, TempLog, TempError, ...
    Protocol/HexProtocolParser.cs # port จาก tcpclient.ParseHexResponse
    Transport/TcpProbeClient.cs, Serial/*.cs
    Services/                     # Polling, Alert, Schedule, Archive, Mqtt, LegacyApi, SerialService
    Endpoints/                    # Devices, Machines, Schedule, TempLogs, Archive, Poll, Sse, Health
  tests/TmsBackend.Tests/         # xUnit (unit)
  installer/tms-backend.iss       # Inno Setup
  build.ps1                       # publish + compile installer
```

---

## Phase 0 — Spike & เตรียมโปรเจกต์
- [x] ติดตั้ง .NET 10 SDK, สร้าง `dotnet/TmsBackend.sln`, โปรเจกต์ `src/TmsBackend` (Web SDK) + `tests/TmsBackend.Tests` (xUnit)
- [x] **Spike tis620**: ต่อ DB จริงด้วย MySqlConnector + `Encoding.RegisterProvider(CodePagesEncodingProvider.Instance)`, `CharacterSet=tis620`, อ่าน/เขียน `machine_name` ภาษาไทยแล้วเทียบกับที่ Go เขียน (ถ้าไม่ผ่าน → fallback ใช้ `SET NAMES` หลังเปิด connection)
- [x] **Spike MariaDB/datetime**: ตรวจ `insert_time` ความละเอียด ms (`yyyy-MM-dd HH:mm:ss.fff`) และ timezone (`SE Asia Standard Time` แทน `Asia/Bangkok`, fallback UTC+7)
- [x] **Spike Serial**: เปิด COM จริงด้วย `SerialPort` ตรวจ DTR/RTS ว่า reset MCU เหมือน go.bug.st (กำหนด `DtrEnable/RtsEnable` ให้ตรง) + boot delay 2s
- [x] ตั้ง `Directory.Build.props`: nullable, warnings-as-errors, `InvariantGlobalization=false`
- [x] `.gitignore` สำหรับ `bin/ obj/ publish/`

## Phase 1 — Foundation (Config, Logging, Hosting)
- [ ] `Program.cs`: ตั้ง `ContentRoot`/working dir = `AppContext.BaseDirectory` (แทน `changeToExeDir`) + fallback `.env` จาก launch dir สำหรับ `dotnet run`
- [ ] โหลด `.env` (DotNetEnv) → bind `AppOptions` ครบทุก key เดิม: `PORT, DB_*, DB_CHARSET, POLL_INTERVAL, ALERT_INTERVAL, LEGACY_API_URL/TOKEN, MQTT_*, DEFAULT_TCP_PORT, DEFAULT_SERIAL_BAUD, DEFAULT_SERIAL_BOOT_DELAY, ARCHIVE_DIR, ARCHIVE_RETENTION_DAYS, ARCHIVE_RUN_HOUR` (ดู `.env.example`)
- [ ] เพิ่ม key ใหม่ `POLLING_AUTOSTART` (default true) สำหรับ e2e
- [ ] Serilog: console format อ่านง่าย, `logs/error_YYYY-MM-DD.txt` เฉพาะ Error (เหมือน `internal/utils/logger.go` รวม cleanup ไฟล์เก่า), Windows EventLog เมื่อรันเป็น service
- [ ] Startup diagnostic log `%TEMP%\tms-backend-startup.log` (cap 200KB) แบบ `startupDiag` ใน `main.go`
- [ ] `UseWindowsService()` — auto-detect: รันจาก SCM = service, double-click = console (Ctrl+C → graceful shutdown, `HostOptions.ShutdownTimeout = 15s`)
- [ ] Version: `AssemblyInformationalVersion`, log ตอนเริ่ม
- [ ] Global exception handling: unhandled exception ใน background loop ไม่ทำให้ process ล้ม (เทียบ `recover()` ใน Go)

## Phase 2 — Data layer (Dapper)
- [ ] Models (record/class) ตาม `internal/models/models.go`: `MasterMachine` (+ helper `GetMinTemp/MaxTemp/AdjTemp`, `IsTemperatureType`, `GetUnit`, `GetScheduleTimes`, `HasScheduleTime`), `TempLog`, `TempError`, `TempLogArchive`, `ArchiveManifest`, `ConfigValue`, DTO `MachineWithStatus`, `DeviceGroup`
- [ ] JSON naming: camelCase ตรงกับ tag เดิม (`machineIp`, `probeNo`, `sType`, ...), null handling เหมือน pointer ใน Go, `MasterUser.Password` ไม่ serialize
- [ ] `DbConnectionFactory`: connection string จาก options, pool (max 100, idle lifetime 5 นาที), ping สำหรับ `/health`
- [ ] Startup connect + retry w/ backoff (5 ครั้ง, 2s→30s) + `WaitForNetwork(60s)` port จาก `internal/utils/network.go`
- [ ] Repositories: Machine, TempLog, TempError, Archive, Config — แปล query จาก `handlers.go` / `polling.go` / `archive_service.go` เป็น SQL ตรง (คงลำดับ/เงื่อนไขเดิม)
- [ ] Insert `temp_log`/`temp_error` เวลาแบบ Thailand time + ms (แทน GORM `BeforeCreate`)
- [ ] Unit tests: port `models_test.go`

## Phase 3 — Device communication
- [ ] `HexProtocolParser` port จาก `tcpclient.ParseHexResponse` ครบ: header `41 41`/`41 42` (ultra-low), 9/12/15/18 bytes, `rawToTemp`, `calcHumidity` (SHT1x), round 2 ตำแหน่ง, `FilterValidProbes`
- [ ] `TcpProbeClient`: connect/write `cmd+\r`/read จนเจอ `0x0D` หรือ timeout, retry สูงสุด 3 ครั้งเมื่อค่าผิดปกติ
- [ ] Serial: `SerialConnection` (persistent, lock, boot settle delay, auto-reconnect 5s, mark disconnected เมื่อ I/O error) + `SerialManager` + `IsSerialPath("COMx")` + protocol `sendOnce/Request` จาก `serialclient/protocol.go`
- [ ] `SerialService` (hosted): เปิดทุก COM จาก `master_machine` ตอนเริ่ม
- [ ] `ProbeRequester.RequestProbes(ip, cmd, timeout)` เลือก TCP/Serial แบบ `polling.go:requestProbes`
- [ ] Unit tests: port `tcpclient_test.go`, `protocol_test.go`, `serialclient_test.go` (ใช้ test vector hex เดิมทั้งหมด)

## Phase 4 — Background services
- [ ] `MqttService` (MQTTnet): connect+retry, `PublishTemperature/Batch`, payload JSON เหมือน `mqtt_service.go`, `IsConnected`
- [ ] `LegacyApiService` (`HttpClient` + typed client): `SendTempLog/Batch/Alert`, token header, timeout เหมือน `api_notification.go`
- [ ] `PollingService` (BackgroundService) port `polling.go`:
  - [ ] poll & save ทุก `POLL_INTERVAL` (+ initial poll ตอนเริ่ม) → insert temp_log, legacy API, notify SSE `refresh`
  - [ ] alert loop ทุก `ALERT_INTERVAL`: `checkAlerts` / `checkProbeAlert` state machine, `alertStates`, `alertLastSent` repeat, `updateChkMon`, MQTT publish, notify SSE `temperature`
  - [ ] `checkMonitoredMachines`, `checkScheduleReports` (ทุก 5 นาที)
  - [ ] machine cache 1 นาที + stale fallback
  - [ ] `TriggerOnce()` สำหรับ `POST /api/poll`
  - [ ] event hub: `Subscribe/Unsubscribe` ด้วย `Channel<T>` (bounded 10, drop เมื่อเต็ม เหมือน Go)
- [ ] `ArchiveService` (BackgroundService, ทุกชั่วโมง, รันวันละครั้งหลัง `ARCHIVE_RUN_HOUR`): `RunArchive`, `archiveDayFromTable`, `ArchiveFromTable`, `RestoreRange`, write+verify file — **รูปแบบไฟล์ JSON/ชื่อไฟล์/manifest ต้องเหมือนเดิม** เพื่ออ่านไฟล์ที่ Go archive ไว้ได้
- [ ] Unit tests: port `polling_test.go`, `archive_service_test.go`, `serial_service_test.go`, `request_probes_test.go` (mock repository แทน sqlmock)

## Phase 5 — HTTP API
- [ ] Endpoints ตาม `internal/handlers/routes.go` (path/method/status code/JSON body ต้องเหมือนเดิม):
  - [ ] `GET /health` (status/db/mqtt/version, 503 เมื่อ DB ล่ม)
  - [ ] Devices: `GET/POST /api/devices`, `GET/PUT/DELETE /api/devices/{id}`
  - [ ] Machines: `GET /api/machines`, `PUT /api/machines/{machineIp}/{probeNo}`
  - [ ] Schedule: `GET/PUT .../schedule`, `POST/DELETE .../schedule/{time}` (ดู `docs/schedule-api.md`)
  - [ ] `GET /api/temp-logs`, `GET /api/reports/templog` (รวม archive cutoff + `applyDeviceFilter`), `GET /api/temp-errors`
  - [ ] Archive: `POST /api/archive/run`, `GET /api/archive`, `POST /api/archive/restore`
  - [ ] `POST /api/poll`
  - [ ] SSE `GET /api/temperature-stream`: event `connected` (+version), `refresh`, `temperature`, heartbeat 30s, unsubscribe เมื่อ client หลุด, ปิดได้ตอน shutdown
- [ ] CORS allow all, request log
- [ ] Unit/integration tests ด้วย `WebApplicationFactory` port จาก `handlers_test.go`

## Phase 6 — Sub-commands & Parity testing
- [ ] `tms-backend.exe archive-table <table>` (แทน `script/main.go`)
- [ ] `tms-backend.exe --version`
- [ ] ปรับ Go e2e suite (`e2e/main_test.go`) ให้รับ `E2E_BASE_URL` → ถ้ากำหนดจะยิงไปยัง server ภายนอก (C#) แทนการ boot Fiber in-process; รัน C# ด้วย `POLLING_AUTOSTART=false` + `.env.e2e` เดียวกัน
- [ ] รัน e2e ทั้งชุดกับ C# ให้ผ่าน (`go test -tags=e2e ./e2e/...`)
- [ ] รัน Postman collection (`postman/`) กับ C# เทียบ response กับ Go
- [ ] ทดสอบคู่ขนาน (shadow) บนเครื่องทดสอบ: Go กับ C# ชี้ DB ทดสอบแยก, อุปกรณ์จริงชุดเดียว, เทียบ temp_log/temp_error/MQTT payload 1–2 วัน
- [ ] Restore ไฟล์ archive ที่ Go สร้างไว้ด้วย C# ได้ถูกต้อง

## Phase 7 — Build & Installer (Inno Setup)
- [ ] `build.ps1`: `dotnet publish -c Release -r win-x64 --self-contained -p:PublishSingleFile=true -p:Version=$Version` (version: env → `.env` VERSION → `git describe` → dev เหมือน `build.sh`) แล้ว compile `.iss` ด้วย `ISCC.exe`
- [ ] `installer/tms-backend.iss`:
  - [ ] ติดตั้งที่ `C:\TMS` (default, เปลี่ยนได้), ต้อง admin
  - [ ] หน้า wizard กรอก PORT / DB_HOST / DB_PORT / DB_USER / DB_PASSWORD / DB_NAME → เขียน `.env` **เฉพาะเมื่อยังไม่มี** (`onlyifdoesntexist`) ไม่ทับ config เดิมตอน upgrade
  - [ ] ก่อน copy ไฟล์: `sc stop TMSBackend` + รอจนหยุด (upgrade ทับ exe ได้)
  - [ ] หลัง copy: `sc create TMSBackend binPath= "C:\TMS\tms-backend.exe" start= delayed-auto DisplayName= "TMS Backend"`, `sc description`, `sc failure TMSBackend reset= 86400 actions= restart/5000/restart/10000/restart/30000`, `sc start`
  - [ ] Firewall rule เปิดพอร์ต (`netsh advfirewall firewall add rule`)
  - [ ] ถ้าพบ Go เวอร์ชันเก่า: ลบ registry Run key ของ tray (`tray.RemoveFromStartup` เดิม) + kill process เก่า, ถ้ามี service NSSM ชื่อ `TMSBackend` เดิมให้ลบก่อน
  - [ ] Start Menu: "Run in console (debug)", "Open logs folder", "Open health check"
  - [ ] Uninstall: stop + `sc delete` + ลบ firewall rule, **เก็บ** `.env`, `logs/`, `archives/` ไว้
- [ ] สิทธิ์ service account: LocalSystem (default) — ตรวจว่าเข้าถึง COM port และเขียน `C:\TMS\logs`, `archives` ได้

## Phase 8 — Cutover & Cleanup
- [ ] Deploy เครื่อง pilot 1 เครื่อง ด้วย setup.exe ทับ Go เดิม ตรวจ: service start หลัง reboot, auto-restart เมื่อ kill process, frontend/SSE ใช้ได้, log ถูกที่
- [ ] Rollout เครื่องที่เหลือ
- [ ] อัปเดต `README.md` (ภาษาไทย): build, ติดตั้ง, console vs service, troubleshooting
- [ ] หลังเสถียร: ลบโค้ด Go (`main.go`, `internal/`, `script/`, `build.sh`, `debug.sh`, `go.mod`) และย้าย e2e เป็น C# (หรือเก็บ Go e2e ไว้เป็น black-box suite)

---

## จุดเสี่ยงที่ต้องระวัง
- **tis620 ภาษาไทย** — ต้องผ่าน spike Phase 0 ก่อนเดินต่อ
- **เวลา/timezone** — Go ใช้ `loc=Local` + Thailand time ใน NowFunc; ต้องให้ค่าใน DB ตรงกันเป๊ะ
- **Serial DTR/RTS** — พฤติกรรม reset MCU ต่างกันระหว่าง library
- **Service ใน Session 0** — ไม่มี UI; ทุกอย่างต้องดูผ่าน log/health endpoint
- **SSE ตอน shutdown** — ต้องยกเลิก stream ด้วย `ApplicationStopping` ไม่ให้ค้าง (เทียบ `ShutdownWithTimeout(5s)`)

## Verification
1. `dotnet test dotnet/TmsBackend.sln` — unit tests ทั้งหมดผ่าน (parser test vectors เดียวกับ Go)
2. `dotnet run --project dotnet/src/TmsBackend` → console แสดง log, `curl http://localhost:8080/health` ได้ `{"status":"ok",...}`, Ctrl+C หยุดภายใน 15s
3. `E2E_BASE_URL=http://localhost:8080 go test -tags=e2e ./e2e/...` ผ่านทั้งหมดกับ C# server
4. ติดตั้ง `setup.exe` บน VM Windows สะอาด → `sc query TMSBackend` = RUNNING, reboot แล้วยังรัน, `taskkill /f` แล้ว service restart เอง, uninstall แล้วไม่เหลือ service แต่ `.env`/logs ยังอยู่
5. Upgrade ทับเวอร์ชันเดิมด้วย setup.exe ใหม่ → `.env` ไม่ถูกทับ, `/health` แสดง version ใหม่
