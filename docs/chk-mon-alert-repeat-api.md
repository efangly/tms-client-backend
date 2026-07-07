# chkReport / chkMon — Repeat Alert Monitoring

ฟีเจอร์นี้ทำให้ probe ที่เปิด `chkReport` ส่ง alert **ซ้ำต่อเนื่อง** ตราบใดที่ค่ายังเกิน threshold (min/max) แทนที่จะส่งครั้งเดียวตอนเปลี่ยน state เหมือน probe ทั่วไป และจะหยุดส่งอัตโนมัติเมื่อค่ากลับเข้าช่วงปกติ

---

## Field ที่เกี่ยวข้อง (ตาราง `master_machine`)

| Field       | Type              | ใครเป็นคนตั้งค่า        | ความหมาย                                                                 |
|-------------|-------------------|--------------------------|----------------------------------------------------------------------------|
| `chkReport` | `string` (`"0"`/`"1"`) | **Frontend** (ผู้ใช้ตั้งค่า) | เปิด/ปิดโหมด "แจ้งเตือนซ้ำจนกว่าจะกลับปกติ" สำหรับ probe นี้ |
| `chkMon`    | `string` (`"0"`/`"1"`) | **Backend เท่านั้น** (read-only ฝั่ง frontend) | สถานะปัจจุบันว่า probe นี้กำลัง "อยู่ในภาวะ alert ต่อเนื่อง" หรือไม่ |

> **หมายเหตุ:** Frontend สามารถ `PUT` แก้ `chkReport` ได้ตามปกติผ่าน endpoint แก้ไข machine เดิม แต่ไม่ควรส่ง `chkMon` มาด้วย เพราะ backend จะ overwrite ค่านี้เองตามผลการตรวจ threshold — ควรใช้แค่แสดงผล (badge/indicator) ใน UI เท่านั้น

---

## พฤติกรรมของระบบ

1. เมื่อ probe ที่ `chkReport = "1"` มีค่าตรวจวัดเกิน `minTemp`/`maxTemp` (state `H` หรือ `L`) → backend จะ:
   - ส่ง alert notification (เหมือน probe ทั่วไป)
   - ตั้ง `chkMon = "1"` ในฐานข้อมูล
2. ตราบใดที่ยังเกิน threshold อยู่ → backend จะส่ง alert **ซ้ำทุกรอบตรวจ** (ทั้งรอบ poll 5 นาที และรอบ alert-check 5 วินาที) ไม่ dedup เหมือน probe ทั่วไป
3. เมื่อค่ากลับเข้าช่วงปกติ (state `N`) → backend จะ:
   - ส่ง notification "กลับเข้าช่วงปกติแล้ว" 1 ครั้ง
   - ตั้ง `chkMon = "0"` กลับ

Probe ที่ `chkReport = "0"` (ค่าเริ่มต้น) จะยังคงพฤติกรรมเดิม คือส่ง alert แค่ตอนเปลี่ยน state เท่านั้น (ไม่ spam ซ้ำ) และไม่แตะ `chkMon`

---

## Endpoints ที่เกี่ยวข้อง

### อ่านค่า `chkReport` / `chkMon` ปัจจุบันของทุกเครื่อง

```
GET /api/machines
```

**Response 200** (ตัดเฉพาะ field ที่เกี่ยวข้อง)
```json
[
  {
    "machineIp": "192.168.1.10",
    "probeNo": 1,
    "machineName": "Server Room A",
    "chkReport": "1",
    "chkMon": "1",
    "minTemp": 18.0,
    "maxTemp": 28.0,
    "sType": "t",
    "currentValue": 31.2,
    "onlineStatus": "Online",
    "lastUpdate": "2026-07-02 10:15:00"
  }
]
```

> ใช้ `chkMon === "1"` เพื่อแสดง badge "กำลังแจ้งเตือนต่อเนื่อง" ใน UI list/dashboard

หรือดึงเครื่องเดียว:
```
GET /api/devices/:machineIp?probeNo=1
```

---

### เปิด/ปิดโหมด chkReport ของ probe

```
PUT /api/machines/:machineIp/:probeNo
Content-Type: application/json
```

**Request Body**
```json
{ "chkReport": "1" }
```

**Response 200** — ข้อมูล machine ที่อัพเดตแล้ว (มี `chkMon` ปัจจุบันติดมาด้วย)

> Endpoint เดียวกันนี้ใช้แก้ field อื่นได้ด้วย (`chkOnline`, `chkSms`, `chkMail`, `chkLine`, `minTemp`, `maxTemp`, `adjTemp`, `machineName`, `color`, `sType`) — ห้ามส่ง `machineIp`/`probeNo` มาแก้ (จะถูก backend ignore)

> Endpoint สำรอง (เดิม, รองรับเหมือนกัน): `PUT /api/devices/:machineIp?probeNo=1`

---

### ดูประวัติ alert (temp_error)

```
GET /api/temp-errors?startDate=2026-07-01&endDate=2026-07-02&limit=100
```

**Response 200**
```json
[
  {
    "machineIp": "192.168.1.10",
    "probeNo": 1,
    "machineName": "Server Room A",
    "tempValue": 31.2,
    "errorTime": "2026-07-02T10:15:00+07:00",
    "minTemp": 18.0,
    "maxTemp": 28.0,
    "tempStatus": "p",
    "errorType": "o",
    "sType": "t"
  }
]
```

> ทุกครั้งที่มีการส่ง alert ซ้ำระหว่างค้างเกิน threshold **ไม่** สร้าง record `temp_error` ใหม่ทุกครั้ง — record จะถูกสร้างเฉพาะตอนเข้าสู่ state ผิดปกติครั้งแรก (unique constraint กันซ้ำ) ดังนั้นถ้าต้องการนับจำนวนครั้งที่ "แจ้งเตือนซ้ำ" ให้ดูจาก log ฝั่ง backend หรือระบบแจ้งเตือนภายนอก (SMS/Email/Line) แทน ไม่ใช่จากตาราง `temp_error`

---

### Real-time status ผ่าน SSE

```
GET /api/temperature-stream
```

Frontend เปิดด้วย `EventSource` เพื่อรับค่าล่าสุดแบบ real-time (poll cycle ทุก ~5 วินาที) แต่ **event นี้ไม่มี field `chkMon`** — ใช้สำหรับอัพเดตกราฟ/ตัวเลขอุณหภูมิเท่านั้น หากต้องการ badge "กำลัง alert ต่อเนื่อง" ให้ polling `GET /api/machines` เป็นระยะ (เช่นทุก 10–30 วินาที) ควบคู่กันไป

**Event: `temperature`**
```json
{
  "type": "temperature",
  "count": 2,
  "lastUpdated": "2026-07-02 10:15:05",
  "data": [
    {
      "machineName": "Server Room A",
      "tempValue": 31.2,
      "status": "H",
      "type": "t",
      "timestamp": "2026-07-02 10:15:05",
      "minTemp": 18.0,
      "maxTemp": 28.0,
      "ipAddress": "192.168.1.10",
      "probeNo": 1
    }
  ]
}
```
`status`: `"N"` = ปกติ, `"H"` = เกินบน, `"L"` = ต่ำกว่าล่าง

**Event: `refresh`** (แจ้งว่ามีการบันทึก log ชุดใหม่ลง DB — ใช้ trigger การ re-fetch รายการ/กราฟ)
```json
{ "type": "refresh", "saved": 4, "errors": 0 }
```

---

## แนวทางออกแบบ UI ที่แนะนำ

- **หน้า Machine List / Dashboard:** เพิ่ม toggle "แจ้งเตือนต่อเนื่อง" ผูกกับ `chkReport` (เขียนผ่าน `PUT /api/machines/:ip/:probeNo`) และแสดง badge สีแดง/กระพริบเมื่อ `chkMon === "1"` (แปลว่ากำลังอยู่ในภาวะเกิน threshold และระบบกำลังแจ้งเตือนซ้ำอยู่)
- **หน้า Detail ของ probe:** แสดงสถานะ `chkMon` แบบ read-only พร้อมข้อความอธิบาย เช่น "อยู่ระหว่างแจ้งเตือนต่อเนื่อง จะหยุดอัตโนมัติเมื่อค่ากลับเข้าช่วงปกติ"
- **ห้าม** ให้ผู้ใช้แก้ `chkMon` เองผ่าน UI — เป็นสถานะที่ backend คำนวณจากค่าจริงเท่านั้น
