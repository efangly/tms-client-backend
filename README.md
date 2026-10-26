# TMS Backend

Backend ของระบบวัดอุณหภูมิ TMS: REST API + SSE สำหรับ frontend, poll อุปกรณ์วัดอุณหภูมิผ่าน TCP และ COM,
บันทึกลง MySQL/MariaDB, ตรวจ alert, ส่ง Legacy API + MQTT, และ archive ข้อมูลเก่าเป็นไฟล์ JSON รายวัน

> **กำลังย้ายจาก Go ไปเป็น C# (.NET 10)** — เวอร์ชัน C# อยู่ในโฟลเดอร์ [`dotnet/`](dotnet/)
> ทำงานแทน Go ได้ทันที (API, DB, ไฟล์ archive, MQTT/Legacy API payload เหมือนเดิม)
> และรันเป็น **Windows Service** ติดตั้งด้วย Setup.exe
> แผนและสถานะ: [`docs/csharp-migration-plan.md`](docs/csharp-migration-plan.md)

## เอกสาร

| เรื่อง | ที่ |
|---|---|
| Build, ติดตั้ง/อัปเกรด/ถอน, config `.env`, console vs service, log, การทดสอบ, troubleshooting | [`dotnet/README.md`](dotnet/README.md) |
| ขั้นตอนเปลี่ยนเครื่องจาก Go → C# (ตรวจ `.env`, pilot, ตรวจหลังติดตั้ง, rollback) | [`dotnet/CUTOVER.md`](dotnet/CUTOVER.md) |
| API ตาราง schedule | [`docs/schedule-api.md`](docs/schedule-api.md) |
| Postman collection | [`postman/`](postman/) |

## เริ่มต้นเร็ว (C#)

```powershell
# build exe + Setup.exe  →  dotnet\dist\tms-backend-setup-<version>.exe
powershell -ExecutionPolicy Bypass -File dotnet\build.ps1

# รันแบบ console ระหว่างพัฒนา (อ่าน .env ที่ root ของ repo)
dotnet run --project dotnet\src\TmsBackend

curl http://localhost:8080/health
```

| วิธีเปิด | โหมด |
|---|---|
| ติดตั้งด้วย Setup.exe | Windows Service `TMSBackend` — เริ่มเองตอนบูต (ไม่ต้อง login), restart เองเมื่อ process ตาย |
| double-click `tms-backend.exe` / Start Menu "console (debug)" | Console — เห็น log สด, Ctrl+C หยุด (ต้องหยุด service ก่อน) |

คำสั่งเสริมของ exe: `--version`, `check-env` (ตรวจ `.env` ก่อน cutover), `probe <ip|COMx>` (อ่านอุปกรณ์ 1 ครั้ง),
`archive-table <table>` (archive ตารางสำรอง)

## การทดสอบ

```powershell
dotnet test dotnet\TmsBackend.sln                                    # unit tests (C#)
powershell -ExecutionPolicy Bypass -File dotnet\e2e.ps1              # e2e suite กับ C# server
go test -tags=e2e -count=1 ./e2e/...                                 # e2e suite กับ Go (baseline)
```

e2e ใช้ DB แยก (`e2e/.env.e2e` — ดู `e2e/.env.e2e.example`) และ **ลบข้อมูลทุกแถว** ในตารางที่ใช้ ห้ามชี้ไปที่ DB จริง

---

## เวอร์ชัน Go (เดิม — ใช้จนกว่าจะ cutover ครบทุกเครื่อง)

โค้ด Go (`main.go`, `internal/`, `script/`) ยังอยู่เพื่อใช้ rollback และเป็น baseline ของ e2e
จะลบออกหลัง C# เสถียรบนทุกเครื่องแล้ว

- Build: `./build.sh` (บน Mac/Linux/Git Bash) → `build/tms-backend.exe` (tray app, ไม่มี console)
- รัน: double-click → ไอคอนใน system tray, คลิกขวา → **Run at Windows Startup** เพื่อให้เปิดเองตอน login
- log error: `logs/error_YYYY-MM-DD.txt`
- ข้อจำกัดที่ C# แก้แล้ว: ไม่ใช่ service จริง (เริ่มหลัง user login เท่านั้น), ไม่สนใจ `POLL_INTERVAL` /
  `ALERT_INTERVAL` / `DEFAULT_*` / `ARCHIVE_*` ใน `.env`, เลิก retry DB หลัง 5 ครั้ง

Rollback จาก C# กลับ Go: ดู [`dotnet/CUTOVER.md`](dotnet/CUTOVER.md#4-rollback-กลับไปใช้-go)
