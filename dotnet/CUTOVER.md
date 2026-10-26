# Cutover: เปลี่ยนเครื่องจาก Go เป็น C#

ทำทีละเครื่อง เริ่มจากเครื่อง pilot 1 เครื่อง แล้วค่อย rollout ที่เหลือ
C# ใช้ `.env`, ตาราง DB, ไฟล์ archive และ API ชุดเดิมทั้งหมด จึงสลับไป–กลับได้โดยไม่ต้องย้ายข้อมูล

## 1. ก่อนติดตั้ง (ทำบนเครื่องปลายทาง)

- [ ] จด **โฟลเดอร์ที่ Go ติดตั้งอยู่** (ที่มี `tms-backend.exe` + `.env`) — ปกติ `C:\TMS`
- [ ] สำรอง `.env` และโฟลเดอร์ `archives\` ไว้ที่อื่น
- [ ] ตรวจ `.env` ด้วย exe ตัวใหม่ (ยังไม่ต้องติดตั้ง — วาง exe ไว้ที่ไหนก็ได้ แล้วสั่งจากโฟลเดอร์ Go เดิม):

  ```bat
  cd C:\TMS
  D:\path\to\new\tms-backend.exe check-env
  ```

  - หัวข้อ **"Different from the Go build"** = ค่าที่เขียนไว้ใน `.env` แต่ Go ไม่เคยใช้
    (`POLL_INTERVAL`, `ALERT_INTERVAL`, `DEFAULT_TCP_PORT`, `DEFAULT_SERIAL_*`, `ARCHIVE_*`)
    → C# จะเริ่มใช้ค่านั้นจริง ถ้าไม่ได้ตั้งใจ ให้แก้หรือลบบรรทัดนั้นออกก่อน
  - หัวข้อ **"Invalid values"** = ค่าผิดรูปแบบ จะใช้ default แทน
  - `Database: NOT reachable` = แก้ `DB_*` ก่อน
  - exit code 0 = พร้อม, 1 = มีรายการต้องดู
- [ ] ถ้ามีอุปกรณ์ COM: ปิดโปรแกรมอื่นที่อาจถือ COM port (Arduino IDE, terminal ฯลฯ)

## 2. ติดตั้ง

- [ ] รัน `tms-backend-setup-<version>.exe` (admin) แล้ว **เลือกโฟลเดอร์เดียวกับ Go เดิม**
  - มี `.env` อยู่แล้ว → หน้าตั้งค่าจะถูกข้าม ใช้ค่าเดิม
  - Setup หยุด/kill Go ตัวเก่า ลบ autostart ของ tray (`HKCU\...\Run\TMS-Backend`) ให้เอง
  - ถ้าต้องลงคนละโฟลเดอร์: copy `.env` และ **`archives\`** ไปที่ใหม่ก่อน — `archive_manifest.file_path`
    เก็บ path แบบ relative (`archives\temp_log\...`) ซึ่งนับจากโฟลเดอร์ exe

## 3. ตรวจหลังติดตั้ง

| ตรวจ | วิธี | ผ่านเมื่อ |
|---|---|---|
| service รันอยู่ | `sc query TMSBackend` | `STATE: 4 RUNNING` |
| health | เปิด `http://localhost:<PORT>/health` | `"status":"ok"`, `"db":true`, `version` เป็นเวอร์ชันใหม่, `"mqtt":true` ถ้าตั้ง MQTT |
| frontend | เปิดหน้าเว็บ TMS ตามปกติ | เห็นเครื่อง/ค่าอุณหภูมิ, กราฟ report ใช้ได้ |
| SSE (realtime) | หน้า dashboard ค้างไว้ ≥ 1 รอบ `POLL_INTERVAL` | ค่าอัปเดตเองโดยไม่ต้อง refresh |
| บันทึกข้อมูล | หลัง 1 รอบ poll: `GET /api/temp-logs?limit=5` | มีแถวใหม่เวลาปัจจุบัน ของทุกเครื่อง/probe |
| Legacy API / MQTT | ฝั่งระบบกลาง / broker | ได้รับข้อมูลรอบใหม่ |
| log | `C:\TMS\logs\error_<วันนี้>.txt` | ไม่มี หรือมีแต่ error ที่อธิบายได้ (เช่นอุปกรณ์ดับจริง) |
| auto-restart | `taskkill /f /im tms-backend.exe` แล้วรอ ~10 วินาที | `sc query` กลับมา RUNNING เอง |
| reboot | restart เครื่อง **ไม่ต้อง login** แล้วเปิด `/health` จากเครื่องอื่น | ตอบ ok (service ขึ้นก่อน login) |
| firewall | เปิด `http://<IP เครื่อง>:<PORT>/health` จากเครื่องอื่น | ตอบได้ |

เครื่อง pilot: ปล่อยไว้ **อย่างน้อย 1–2 วัน** ให้ผ่านรอบ archive (`ARCHIVE_RUN_HOUR`, default ตี 2) และ schedule report
แล้วตรวจว่า `archive_manifest` มีวันใหม่ และไม่มี error ซ้ำ ๆ ใน log ก่อน rollout ที่เหลือ

## 4. Rollback (กลับไปใช้ Go)

ข้อมูลทุกอย่างใช้ร่วมกันได้ (C# เขียนไฟล์ archive แบบเดียวกับ Go ทุก byte) จึงย้อนได้ทันที:

1. Control Panel → Uninstall **TMS Backend** (ลบ service + firewall rule; `.env`, `logs\`, `archives\` ยังอยู่)
2. วาง `tms-backend.exe` ของ Go กลับในโฟลเดอร์เดิม แล้ว double-click
3. คลิกขวาที่ tray icon → ติ๊ก **Run at Windows Startup**

## 5. Rollout ที่เหลือ

ติดตั้งแบบเงียบได้ (เครื่องที่มี `.env` อยู่แล้ว ไม่ต้องใส่พารามิเตอร์):

```bat
tms-backend-setup-<version>.exe /VERYSILENT /SUPPRESSMSGBOXES /DIR=C:\TMS /LOG=C:\TMS\setup.log
```

ทุกเครื่องยังต้องทำข้อ 1 (`check-env`) และตรวจข้อ 3 อย่างน้อย health + frontend + log

## ติดตามสถานะ

| เครื่อง / site | โฟลเดอร์ | check-env | ติดตั้ง | ตรวจผ่าน | หมายเหตุ |
|---|---|---|---|---|---|
| (pilot) | | | | | |
