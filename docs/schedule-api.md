# Schedule Report API

ระบบส่งรายงานอุณหภูมิอัตโนมัติตามเวลาที่กำหนด โดยเก็บข้อมูลใน field `color` ของตาราง `master_machine`

---

## ข้อกำหนด

- กำหนดเวลาได้สูงสุด **6 เวลา** ต่อ probe
- รูปแบบเวลา: **HHmm** (4 หลัก) เช่น `0800`, `1030`, `1500`
- นาทีต้องเป็นทวีคูณของ 5 เท่านั้น (`00`, `05`, `10`, ..., `55`)
- ระบบจะส่งรายงานโดยอัตโนมัติทุก 5 นาที หากเวลาตรงกับที่ตั้งไว้

---

## Endpoints

Base URL: `/api/machines/:machineIp/:probeNo/schedule`

---

### GET — ดู Schedule ปัจจุบัน

```
GET /api/machines/:machineIp/:probeNo/schedule
```

**Response 200**
```json
{
  "times": ["0800", "1200", "1700"]
}
```

> `times` จะเป็น `[]` หรือ `null` หากยังไม่ได้ตั้งค่า

---

### PUT — ตั้งค่า Schedule ทั้งหมด (แทนที่ค่าเดิม)

```
PUT /api/machines/:machineIp/:probeNo/schedule
Content-Type: application/json
```

**Request Body**
```json
{
  "times": ["0800", "1200", "1700"]
}
```

| Field   | Type       | Required | Description                       |
|---------|------------|----------|-----------------------------------|
| `times` | `string[]` | ✓        | รายการเวลา HHmm, ไม่เกิน 6 รายการ |

**Response 200** — schedule ที่บันทึกแล้ว (deduplication อัตโนมัติ)
```json
{
  "times": ["0800", "1200", "1700"]
}
```

**Response 400** — validation error
```json
{ "error": "invalid time format: 0801 (expected HHmm, e.g. 0800)" }
```
```json
{ "error": "maximum 6 schedule times allowed" }
```

> ส่ง `{"times": []}` เพื่อล้างค่าทั้งหมด

---

### POST — เพิ่มเวลาเดียว

```
POST /api/machines/:machineIp/:probeNo/schedule/:time
```

**Path Parameter**

| Parameter | Description              | Example |
|-----------|--------------------------|---------|
| `:time`   | เวลาในรูปแบบ HHmm        | `0800`  |

**Response 200** — schedule ที่อัพเดตแล้ว
```json
{
  "times": ["0800", "1200"]
}
```

**Response 400**
```json
{ "error": "invalid time format: 0801 (expected HHmm, e.g. 0800)" }
```
```json
{ "error": "maximum 6 schedule times allowed" }
```

> หากเพิ่มเวลาที่มีอยู่แล้ว จะ return schedule เดิมโดยไม่เกิด error

---

### DELETE — ลบเวลาเดียว

```
DELETE /api/machines/:machineIp/:probeNo/schedule/:time
```

**Path Parameter**

| Parameter | Description              | Example |
|-----------|--------------------------|---------|
| `:time`   | เวลาที่ต้องการลบ HHmm    | `0800`  |

**Response 200** — schedule หลังลบแล้ว
```json
{
  "times": ["1200", "1700"]
}
```

> หากลบเวลาที่ไม่มีอยู่ จะ return schedule เดิมโดยไม่เกิด error

---

## ตัวอย่าง Error ทั้งหมด

| HTTP | Body | สาเหตุ |
|------|------|--------|
| 404  | `{"error": "Machine not found"}` | ไม่พบ machine ด้วย IP + probeNo |
| 400  | `{"error": "invalid time format: ..."}` | รูปแบบเวลาผิด หรือนาทีไม่ใช่ทวีคูณของ 5 |
| 400  | `{"error": "maximum 6 schedule times allowed"}` | เกินจำนวน schedule ที่อนุญาต |
| 500  | `{"error": "internal server error"}` | ข้อผิดพลาดฝั่ง server |

---

## เวลาที่อนุญาต (ตัวอย่าง)

| ✓ Valid | ✗ Invalid |
|--------|----------|
| `0800` | `0801` (นาทีไม่ใช่ทวีคูณ 5) |
| `0805` | `800` (ต้อง 4 หลักเสมอ) |
| `1030` | `2400` (ชั่วโมงเกิน 23) |
| `1200` | `1260` (นาทีเกิน 59) |
| `2355` | `abc0` (ไม่ใช่ตัวเลข) |

---

## พฤติกรรมการส่งรายงาน

- ระบบตรวจสอบ schedule **ทุก 5 นาที** แยกจากการ poll อุณหภูมิ
- เมื่อเวลาตรง → ดึงค่าล่าสุดจาก `temp_log` แล้วส่งรายงานผ่าน Alert API
- หากไม่มีข้อมูลอุณหภูมิล่าสุด (เครื่องออฟไลน์) → **ข้ามการส่ง** โดยไม่เกิด error
- รูปแบบ message ที่ส่ง: `[รายงาน] Temperature {MachineName}({ProbeNo}) ค่าปัจจุบัน: {temp}°C ช่วง: {min}-{max}°C {date} {time}`

---

## ตัวอย่างการใช้งาน (JavaScript)

```js
const BASE = 'http://localhost:8080/api'

// ดู schedule ของเครื่อง 192.168.1.10 probe 1
const res = await fetch(`${BASE}/machines/192.168.1.10/1/schedule`)
const { times } = await res.json()
// times = ["0800", "1200", "1700"]

// ตั้ง schedule ใหม่ทั้งหมด
await fetch(`${BASE}/machines/192.168.1.10/1/schedule`, {
  method: 'PUT',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ times: ['0800', '1200', '1700'] }),
})

// เพิ่มเวลา 2000
await fetch(`${BASE}/machines/192.168.1.10/1/schedule/2000`, {
  method: 'POST',
})

// ลบเวลา 1200
await fetch(`${BASE}/machines/192.168.1.10/1/schedule/1200`, {
  method: 'DELETE',
})

// ล้าง schedule ทั้งหมด
await fetch(`${BASE}/machines/192.168.1.10/1/schedule`, {
  method: 'PUT',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ times: [] }),
})
```
