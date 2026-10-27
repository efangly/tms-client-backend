# TMS Backend (C# / .NET 10)

พอร์ตของ `tms-backend` จาก Go มาเป็น C# — API, รูปแบบข้อมูลใน DB, ไฟล์ archive,
payload MQTT และ Legacy API **เหมือนเดิมทุกอย่าง** frontend และระบบอื่นใช้ต่อได้ทันที

exe ตัวเดียวทำงานได้ 2 แบบ:

| วิธีเปิด | โหมด | หมายเหตุ |
|---|---|---|
| double-click / รันจาก terminal | **Console** | เห็น log สด, Ctrl+C = หยุดอย่างนุ่มนวล (รอสูงสุด 15 วินาที) |
| ติดตั้งเป็น service `TMSBackend` | **Windows Service** | เริ่มเองตอนบูต, restart เองเมื่อ process ตาย, log สำคัญลง Event Log |

ไม่มี tray icon แล้ว (Go เดิมใช้ tray + registry Run key)

แผนการย้ายระบบและสถานะแต่ละ phase: [`docs/csharp-migration-plan.md`](../docs/csharp-migration-plan.md)

---

## โครงสร้างโปรเจกต์

```
dotnet/
  TmsBackend.sln
  Directory.Build.props        # net10.0, nullable, warnings = errors
  build.ps1                    # test → publish single-file → Setup.exe
  e2e.ps1                      # รัน Go e2e suite กับ C# server
  installer/                   # tms-backend.iss (Inno Setup), console-debug.cmd
  src/TmsBackend/              # exe หลัก (AssemblyName = tms-backend)
    Program.cs                 # host, โหมด console/service, sub-commands
    Config/                    # AppOptions (.env → ค่า config), GoDuration, EnvFile
    Infrastructure/            # startup (รอ network/DB), ThaiTime, version, sub-commands
    Logging/                   # logs/error_YYYY-MM-DD.txt
    Models/                    # MasterMachine, TempLog, TempError, ...
    Json/                      # GoJson: serialize ให้ตรงกับ encoding/json ของ Go
    Data/                      # Db + repositories (Dapper + MySqlConnector)
    Protocol/                  # HexProtocolParser (frame 41 41 / 41 42)
    Transport/                 # TcpProbeClient, Serial/* (COM แบบเปิดค้าง + reconnect)
    Services/                  # Polling/Alert/Schedule, Archive, MQTT, Legacy API, EventHub (SSE)
    Endpoints/                 # REST API + SSE /api/temperature-stream
  tests/TmsBackend.Tests/      # xUnit (port มาจาก test ของ Go)
  spikes/                      # ผลทดลอง Phase 0 (tis620, datetime, serial) — ดู spikes/README.md
```

เครื่องมือเทียบผลกับ Go อยู่ที่ root ของ repo: `tools/parity-goapi/` และ `tools/parity-compare.cs`

---

## สิ่งที่ต้องมี

- .NET SDK 10 (`winget install Microsoft.DotNet.SDK.10`)
  ถ้า `dotnet` ไม่อยู่ใน PATH ให้เรียก `"C:\Program Files\dotnet\dotnet.exe"` แทน
- MySQL/MariaDB ที่มีตาราง `master_machine`, `temp_log`, `temp_error`, `temp_log_archive`, `archive_manifest`
- (เฉพาะรัน e2e) Go toolchain

## Build / รัน / ทดสอบ

```powershell
cd dotnet
dotnet build TmsBackend.sln              # warning = error
dotnet test  TmsBackend.sln              # unit tests ทั้งหมด
dotnet run --project src/TmsBackend      # console mode, อ่าน .env จาก root ของ repo
```

`dotnet run` วาง exe ไว้ใน `bin/` จึงหา `.env` ข้าง exe ก่อน แล้วค่อย fallback ไปที่โฟลเดอร์ที่สั่งรัน

ตรวจว่า server ขึ้น: `curl http://localhost:8080/health` → `{"db":true,"mqtt":false,"status":"ok","version":"dev"}`
(DB ต่อไม่ได้ = HTTP 503 พร้อม `"status":"degraded"`)

build ในเครื่องจะแสดง version เป็น `dev` เหมือน Go ส่วน build ที่ปล่อยจริงให้ส่ง `-p:Version=<เลขเวอร์ชัน>`
(จะทำใน `build.ps1` ใน Phase 7)

---

## Build ตัวติดตั้ง (Setup.exe)

ต้องมี Inno Setup 6 (`winget install JRSoftware.InnoSetup --scope user`)

```powershell
powershell -ExecutionPolicy Bypass -File dotnet\build.ps1 [-Version 1.2.3] [-SkipTests] [-SkipInstaller]
```

1. รัน unit tests
2. `dotnet publish` แบบ single-file + self-contained (เครื่องปลายทางไม่ต้องลง .NET) → `dotnet\publish\tms-backend.exe`
3. ตรวจว่า exe รายงาน version ถูก
4. compile `installer\tms-backend.iss` → `dotnet\dist\tms-backend-setup-<version>.exe`

Version: `-Version` / env `VERSION` → `VERSION=` ใน `.env` ของ repo → `git describe` → `dev` (เหมือน `build.sh`)

## ติดตั้ง / อัปเกรด / ถอนการติดตั้ง

Setup.exe ต้องรันแบบ admin สิ่งที่ทำ:

| ขั้นตอน | รายละเอียด |
|---|---|
| ก่อน copy ไฟล์ | `net stop TMSBackend` (รอจนหยุด), kill `tms-backend.exe` ที่ค้าง (รวม Go tray ตัวเก่า), ลบ autostart ของ Go (`HKCU\...\Run\TMS-Backend` + `tms-backend-startup.vbs`) |
| copy | `tms-backend.exe`, `console-debug.cmd` ไปที่ `C:\TMS` (เปลี่ยนได้) |
| `.env` | เขียนจากหน้า wizard (Server/DB และ Legacy API/MQTT) **เฉพาะเมื่อยังไม่มี** — อัปเกรดแล้ว config เดิมไม่ถูกทับ และหน้า wizard จะถูกข้าม |
| service | `TMSBackend` (LocalSystem, Automatic (Delayed Start)), restart อัตโนมัติ 5s/10s/30s เมื่อ process ตายหรือออกด้วย error — ถ้ามี service ชื่อนี้อยู่แล้ว (เช่น NSSM ครอบ Go) จะเปลี่ยน binPath แทนการสร้างใหม่ |
| firewall | rule ขาเข้า "TMS Backend" TCP ตาม `PORT` |
| Start Menu | console (debug), เปิดโฟลเดอร์ logs, แก้ `.env`, health check |

ติดตั้งแบบเงียบ (rollout หลายเครื่อง):

```bat
tms-backend-setup-1.2.3.exe /VERYSILENT /SUPPRESSMSGBOXES /DBHOST=10.0.0.5 /DBPASSWORD=xxx /DBNAME=tms_std
```

พารามิเตอร์: `/PORT /DBHOST /DBPORT /DBUSER /DBPASSWORD /DBNAME /LEGACYURL /LEGACYTOKEN /MQTTBROKER /MQTTTOPIC /MQTTCLIENTID`
(ใช้เฉพาะตอนยังไม่มี `.env`) — log ของ setup: `/LOG=C:\TMS\setup.log`

ถอนการติดตั้ง: หยุดและลบ service + firewall rule — **`.env`, `logs\`, `archives\` ยังอยู่**

คำสั่งที่ใช้บ่อย (admin):

```bat
sc query TMSBackend
net stop TMSBackend
net start TMSBackend
```

แก้ `.env` แล้วต้อง restart service ถึงจะมีผล

---

## Config (`.env`)

อ่าน `.env` ข้าง exe (fallback: โฟลเดอร์ที่สั่งรัน) — ตัวแปร environment จริงมีสิทธิ์เหนือกว่า `.env` เสมอ
ค่าที่ไม่ถูกต้องจะใช้ค่า default และเขียน warning ลง log

| Key | Default | หมายเหตุ |
|---|---|---|
| `PORT` | `8080` | |
| `DB_HOST`, `DB_PORT`, `DB_USER`, `DB_PASSWORD`, `DB_NAME` | –, `3306` | |
| `DB_CHARSET` | `tis620` | แสดงใน log เท่านั้น (ดู "ต่างจาก Go") |
| `POLL_INTERVAL` | `5m` | รูปแบบ Go duration: `30s`, `5m`, `1h30m` |
| `ALERT_INTERVAL` | `5s` | |
| `POLLING_AUTOSTART` | `true` | **ใหม่** — `false` = ไม่ poll/archive อัตโนมัติ (ใช้กับ e2e; สั่งผ่าน `POST /api/poll` ได้) |
| `DEFAULT_TCP_PORT` | `8899` | พอร์ตของ probe TCP |
| `DEFAULT_SERIAL_BAUD` | `9600` | |
| `DEFAULT_SERIAL_BOOT_DELAY` | `2` | วินาที รอ MCU บูตหลังเปิด COM |
| `LEGACY_API_URL`, `LEGACY_API_TOKEN` | ว่าง | ว่างตัวใดตัวหนึ่ง = ปิดการส่ง |
| `MQTT_BROKER`, `MQTT_PORT`, `MQTT_CLIENT_ID`, `MQTT_USERNAME`, `MQTT_PASSWORD`, `MQTT_TOPIC` | ว่าง, `1883`, สุ่ม, –, –, `tms/temperature` | `MQTT_BROKER` ว่าง = ปิด MQTT |
| `ARCHIVE_DIR` | `./archives` | relative = นับจากโฟลเดอร์ exe |
| `ARCHIVE_RETENTION_DAYS` | `30` | |
| `ARCHIVE_RUN_HOUR` | `2` | archive วันละครั้งหลังชั่วโมงนี้ |

> **ก่อน cutover ให้ตรวจ `.env` ของทุก site** — Go ไม่สนใจ `POLL_INTERVAL`, `ALERT_INTERVAL`,
> `DEFAULT_*`, `ARCHIVE_*` ใน `.env` (ใช้ค่า hardcode/default) แต่ C# ใช้ค่าตาม `.env` จริง
> ถ้า site ไหนใส่ค่าแปลก ๆ ไว้ พฤติกรรมจะเปลี่ยนทันทีที่สลับมาใช้ C#

---

## Sub-commands

```text
tms-backend.exe                        # รัน server (console หรือ service)
tms-backend.exe --version              # พิมพ์ version แล้วออก (-v ก็ได้)
tms-backend.exe check-env              # ตรวจ .env: ค่าที่ใช้จริง (ซ่อน secret), ค่าผิดรูปแบบ, ค่าที่ต่างจาก Go, ping DB
tms-backend.exe probe <ip|COMx> [cmd]  # อ่านอุปกรณ์ 1 ครั้ง แสดง frame ดิบ + ค่าที่ parse ได้ (cmd default "A")
tms-backend.exe archive-table <table>  # archive ตารางสำรองที่ schema เหมือน temp_log (แทน script/main.go)
```

- `check-env` ใช้ก่อน cutover แต่ละเครื่อง (ดู [`CUTOVER.md`](CUTOVER.md)) — อ่าน `.env` ข้าง exe ก่อน
  ถ้าไม่มีจะใช้ `.env` ในโฟลเดอร์ที่สั่งรัน จึงสั่งจากโฟลเดอร์ Go เดิมได้โดยไม่ต้องติดตั้ง; exit code 0 = พร้อม, 1 = มีรายการต้องดู
- `probe` ใช้ตรวจหน้างานโดยไม่ต้องเปิด server — **ถ้าจะทดสอบ COM ต้องหยุด service ก่อน**
  เพราะ COM port เปิดได้ทีละ process
- `archive-table` รับชื่อตารางจาก `BACKUP_TABLE_NAME` ได้เหมือน script เดิม ไฟล์ที่ได้อยู่ใน series
  `temp_log` ปกติ (report/restore เห็นทันที) และ **ลบแถวในตารางต้นทาง** ของแต่ละวันหลังตรวจไฟล์ผ่าน

---

## Log

| ที่ | อะไร |
|---|---|
| console (stdout) | ทุกระดับ บรรทัดเดียวต่อ event รวม request log แบบ Fiber |
| `logs/error_YYYY-MM-DD.txt` (ข้าง exe) | เฉพาะ Error ขึ้นไป, ลบไฟล์เก่ากว่า 30 วันอัตโนมัติ |
| Windows Event Log (Application, source `TMSBackend`) | Warning ขึ้นไป — เฉพาะตอนรันเป็น service |
| `%TEMP%\tms-backend-startup.log` และ `startup_debug.log` | ขั้นตอนตอนเริ่มโปรแกรม (จำกัด 200KB) ใช้ดูว่าทำไม service ไม่ขึ้น |
| SSE `GET /api/log-stream` | Information ขึ้นไป แบบสด ทั้งโหมด console และ service — เก็บ 500 บรรทัดล่าสุดไว้ในหน่วยความจำ |

### Log ผ่าน SSE (`/api/log-stream`)

ใช้แสดง log ในหน้า frontend ได้โดยไม่ต้องหยุด service (ไม่มีใน Go)

| query | ค่า |
|---|---|
| `level` | `info` (default), `warning`, `error`, `critical` — ส่งเฉพาะระดับนั้นขึ้นไป |
| `history` | จำนวน log ย้อนหลังที่ส่งตอนเชื่อมต่อ `0`–`500` (default `100`) |

- event แรก `{"type":"connected","version":"..."}` ตามด้วย log ทีละ event:
  `{"category":"PollingService","exception":null,"id":42,"level":"warning","message":"...","time":"2026-10-26 17:06:28.756","type":"log"}`
  (`exception` = stack trace เมื่อมี)
- ทุก event มี `id:` — `EventSource` ต่อใหม่เองพร้อม `Last-Event-ID` แล้ว server ส่งต่อจากบรรทัดถัดไป
  (ไม่ซ้ำ ไม่ขาด ถ้ายังอยู่ใน buffer 500 บรรทัด); `id` ที่กระโดดแปลว่ามี log หลุด (client ช้าเกิน 256 บรรทัด)
- heartbeat `: heartbeat` ทุก 30 วินาที เหมือน `/api/temperature-stream`
- **API ไม่มี auth** ใครเข้าถึงพอร์ตได้ก็เห็น log (IP อุปกรณ์, ข้อความ error ของ DB) — ไม่มีรหัสผ่าน/token ใน log

```js
const es = new EventSource(`${API}/api/log-stream?level=info&history=200`);
es.onmessage = (ev) => {
  const e = JSON.parse(ev.data);
  if (e.type === 'log') appendLine(`${e.time} [${e.level}] ${e.category}: ${e.message}`);
};
```

---

## การทดสอบ

### Unit tests
`dotnet test TmsBackend.sln` — port มาจาก test ของ Go ทั้งหมด (parser ใช้ test vector hex ชุดเดียวกัน),
endpoint ทดสอบด้วย TestHost + repository ปลอม

### Restore ไฟล์ archive ของ Go ลง DB (เขียน DB — ใช้ DB ทดสอบเท่านั้น)

```powershell
$env:TMS_ARCHIVE_SMOKE = 'D:\dev\tms-client-backend\e2e\.env.e2e'   # DB_NAME ต้องมีคำว่า test หรือ e2e
$env:TMS_GO_ARCHIVE_DIR = 'D:\dev\tms-client-backend\archives\temp_log'
dotnet test TmsBackend.sln --filter GoArchiveRestoreSmoke
```

ใช้ไฟล์ล่าสุด 3 ไฟล์: ลง manifest → `RestoreRangeAsync` → เทียบทุกแถวใน `temp_log_archive` กับไฟล์ แล้วล้างทิ้ง

### E2E (Go black-box suite กับ C# server)

```powershell
powershell -ExecutionPolicy Bypass -File dotnet\e2e.ps1 [-Port 18090] [-NoBuild]
```

- ใช้ `e2e/.env.e2e` ไฟล์เดียวกับที่ Go suite ใช้ (คัดลอกจาก `e2e/.env.e2e.example`) ซึ่งต้องชี้ไปที่
  **schema สำหรับ e2e โดยเฉพาะ** — test จะ `DELETE` ทุกแถวของ `master_machine`, `temp_log`,
  `temp_error`, `temp_log_archive`, `archive_manifest`
- สคริปต์ build C# แล้วสตาร์ทจากโฟลเดอร์ temp ที่มี `.env` = สำเนา `.env.e2e` ที่ **ตัดบรรทัด `MQTT_*` และ
  `LEGACY_API_*` ออก** (ไม่ส่งอะไรออกไปข้างนอก), ตั้ง `POLLING_AUTOSTART=false`,
  `ARCHIVE_DIR` = โฟลเดอร์ temp, แล้วรัน `go test -tags=e2e ./e2e/...` ด้วย
  `E2E_BASE_URL` / `E2E_ARCHIVE_DIR`
- ไม่ตั้ง `E2E_BASE_URL` = suite จะบูต Go (Fiber) in-process เหมือนเดิม ใช้เป็น baseline เทียบได้:
  `go test -tags=e2e -count=1 ./e2e/...`
- อุปกรณ์จำลอง (`e2e/fake_probe.go`) ฟังที่ `0.0.0.0:8899` — พอร์ตนี้ต้องว่าง
- log ของ C# server ระหว่าง test อยู่ใน `%TEMP%\tms-cs-e2e-<pid>\server.out.log`

### เทียบ response กับ Go บน DB จริง (อ่านอย่างเดียว)

```powershell
go build -o $env:TEMP\parity-goapi.exe ./tools/parity-goapi
& $env:TEMP\parity-goapi.exe -port 18081                       # Go: route อย่างเดียว ไม่ poll
# MQTT_BROKER ต้องไม่ว่าง (บน Windows ค่าว่าง = ลบตัวแปร แล้วจะไปใช้ broker จริงจาก .env)
$env:PORT=18080; $env:POLLING_AUTOSTART='false'; $env:MQTT_BROKER='127.0.0.1'
dotnet run --project dotnet/src/TmsBackend                     # C#
dotnet run tools/parity-compare.cs -- http://localhost:18081 http://localhost:18080
```

ใช้เฉพาะ GET — ระวัง report/temp-logs ช่วงที่เก่ากว่า retention หรือ `includeArchive=true`
จะ restore ไฟล์ archive ลง `temp_log_archive` (เป็นการเขียน DB)

### ผลการทดสอบล่าสุด

| รายการ | ผล |
|---|---|
| Unit tests | 189/189 ผ่าน |
| E2E suite (7 tests) กับ C# | ผ่านทั้งหมด — ชุดเดียวกับที่ Go in-process ผ่าน |
| GET endpoints เทียบ Go บน DB dev (22 รายการ) + SSE `connected` | เหมือนกันทุกรายการ |
| ไฟล์ archive ที่ Go สร้าง (2,311 ไฟล์ / 2,024,988 แถว) | C# อ่านได้ครบ, serialize กลับได้ byte-identical |
| Restore ไฟล์ Go ลง `temp_log_archive` (3 วัน / 2,656 แถว, DB e2e) | ตรงกับไฟล์ทุกแถวทุก field |
| อุปกรณ์จริง TCP `192.168.1.111` และ `COM4` | อ่านค่าได้ถูกต้อง (ผ่านคำสั่ง `probe`) |

---

## ต่างจาก Go (ตั้งใจ)

- **ใช้ค่าใน `.env` ทุก key** (Go ไม่สนใจบาง key — ดูตาราง config)
- **การเชื่อมต่อ DB เป็น utf8mb4** — MySqlConnector ไม่รองรับ tis620 ที่ระดับ connection
  server แปลงตาม charset ของแต่ละ column เอง ภาษาไทยอ่าน/เขียนถูกต้อง (ทดสอบแล้วใน spike)
- **ต่อ DB ไม่ได้ตอนเริ่ม** → retry 5 ครั้ง (2s→30s) แล้ว retry ทุก 30 วินาทีไปเรื่อย ๆ (Go เลิกหลัง 5 ครั้ง)
  ระหว่างนั้น `/health` ตอบ 503
- **MQTT** reconnect เองเสมอ แม้การเชื่อมต่อครั้งแรกจะล้มเหลว
- **Serial**: คำสั่งบน COM เดียวกันเข้าคิวทีละคำสั่ง และล้าง input buffer ก่อนส่งทุกครั้ง (กันค่าค้างจากรอบก่อน)
- **`PUT /api/devices/{id}`** รับเฉพาะ column ที่รู้จัก (whitelist) — key แปลกถูกปฏิเสธก่อนถึง SQL
  (Go ส่งชื่อ key ไปเป็นชื่อ column ตรง ๆ)
- **ลำดับ series ใน `/api/reports/templog`** เรียงตามลำดับที่เจอข้อมูล (Go สุ่มเพราะวนจาก map)
- **บังคับ invariant culture** — เครื่องที่ตั้ง locale ไทยจะไม่ได้ปี พ.ศ. ใน log/วันที่
- **เพิ่ม `GET /api/log-stream`** (SSE ของ log) — endpoint เดิมทั้งหมดไม่เปลี่ยน
- `MQTT_BROKER` ว่าง → ปิด MQTT เฉย ๆ (บั๊กที่ e2e เจอและแก้แล้ว: เดิม C# ล้มตอนเริ่ม)

## Troubleshooting

| อาการ | ตรวจ |
|---|---|
| service ไม่ขึ้น / ขึ้นแล้วหยุด | `%TEMP%\tms-backend-startup.log` (ของ LocalSystem คือ `C:\Windows\Temp`), Event Viewer → Application, `logs\error_*.txt` |
| `/health` ได้ 503 | DB ต่อไม่ได้ — ดู `DB_*` ใน `.env` และ log บรรทัด `Database connection` |
| COM port เปิดไม่ได้ (Access denied) | มี process อื่นถือ COM อยู่ (Go ตัวเก่า, Arduino IDE, `probe` command) — COM เปิดได้ทีละ process |
| ไม่มีข้อมูลจาก probe TCP | `tms-backend.exe probe <ip>` ดู frame ดิบ, ตรวจ `DEFAULT_TCP_PORT` |
| พอร์ต 8080 ถูกใช้ | เปลี่ยน `PORT` ใน `.env` หรือหยุด Go ตัวเก่า |

## ยังไม่ทำ

- ทดสอบ Setup.exe ติดตั้งจริงเป็น service (ต้องใช้ admin): reboot, auto-restart, upgrade, uninstall — Phase 7
- shadow run คู่กับ Go บนเครื่องทดสอบ 1–2 วัน และทดสอบ poll จริงกับ Legacy API/MQTT จริง — Phase 6/4
- pilot และ rollout ตาม [`CUTOVER.md`](CUTOVER.md) — Phase 8
- ลบโค้ด Go หลัง C# เสถียรทุกเครื่อง (e2e suite ใน `e2e/` ใช้ package ของ Go อยู่ ต้องย้าย/ปรับก่อน)
