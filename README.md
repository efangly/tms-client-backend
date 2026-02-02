# TMS Backend - Console Application

Go Backend API สำหรับระบบ TMS แบบ Console Application

## 🚀 คุณสมบัติ

- ✅ Console Window แสดง logs แบบ real-time
- ✅ ปิดหน้าต่าง = หยุดโปรแกรม
- ✅ Graceful shutdown เมื่อกด Ctrl+C
- ✅ Cross-platform build (build บน Mac สำหรับ Windows)

## 📦 Build Instructions

```bash
./build.sh
```

Output: `build/tms-backend.exe`

## 🛠️ การติดตั้ง

### 1. สร้างไฟล์ .env

```env
# Server Configuration
PORT=8080

# Database Configuration
DB_HOST=localhost
DB_PORT=3306
DB_USER=root
DB_PASSWORD=yourpassword
DB_NAME=tms

# Polling Configuration
POLL_INTERVAL=5m
ALERT_INTERVAL=5s
```

### 2. Deploy ไปยัง Windows

```
C:\TMS\
├── tms-backend.exe
└── .env
```

### 3. รันโปรแกรม

**Double-click** `tms-backend.exe` หรือรันใน Command Prompt:

```cmd
cd C:\TMS
tms-backend.exe
```

Console window จะปรากฏพร้อม logs:
```
Starting TMS Backend Server on port 8080
Press Ctrl+C or close this window to stop the server
```

## 🛑 การหยุดโปรแกรม

มี 2 วิธี:

### 1. ปิดหน้าต่าง Console (แนะนำ)
- คลิกปุ่ม X ที่มุมบนขวา
- โปรแกรมจะหยุดทันที

### 2. กด Ctrl+C
- กดใน Console Window
- โปรแกรมจะทำ graceful shutdown (ปิด connections อย่างถูกต้อง)

## 📡 API Endpoints

เมื่อเซิร์ฟเวอร์ทำงาน:

- Health Check: `http://localhost:8080/health`
- API Base: `http://localhost:8080/api`

### ทดสอบการทำงาน:
```cmd
curl http://localhost:8080/health
```
ควรได้: `{"status":"ok"}`

## 🐛 Troubleshooting

### ไม่สามารถเชื่อมต่อ Database
```
Failed to connect to database: dial tcp :3306: connectex
```

**แก้ไข:**
1. ตรวจสอบว่า MySQL กำลังทำงาน
2. ตรวจสอบ credentials ในไฟล์ `.env`
3. สร้าง database:
   ```sql
   CREATE DATABASE tms;
   ```

### Port ถูกใช้แล้ว
```
bind: address already in use
```

**แก้ไข:**
- ตรวจสอบ port: `netstat -ano | findstr :8080`
- เปลี่ยน PORT ในไฟล์ `.env`

### Console ไม่แสดง
- ตรวจสอบว่าไม่ได้ minimize ไป taskbar
- ลองรันใน Command Prompt แทน double-click

## 📋 Console Output

**เมื่อเริ่มต้น:**
```
Starting TMS Backend Server on port 8080
Press Ctrl+C or close this window to stop the server

[INFO] GET /health - 200 OK
[INFO] GET /api/devices - 200 OK
```

**เมื่อกด Ctrl+C:**
```
^C
Shutting down gracefully...
Goodbye!
```

## ⚡ Windows Service (Optional)

ถ้าต้องการให้ทำงานเป็น Background Service ใช้ NSSM:

```cmd
# ติดตั้ง NSSM
winget install NSSM

# สร้าง Service
nssm install TMSBackend "C:\TMS\tms-backend.exe"
nssm set TMSBackend AppDirectory "C:\TMS"
nssm start TMSBackend
```

## 🔧 Development

### Build สำหรับ Windows (จาก Mac)
```bash
./build.sh
```

### Build สำหรับ Mac/Linux
```bash
go build -o tms-backend ./main.go
```

### Run Local
```bash
go run main.go
```

## 📊 Features

- REST API เพื่อจัดการ devices, temperature logs
- Real-time SSE (Server-Sent Events) streaming
- Background polling service
- CORS support
- Graceful shutdown
- **Error logging to file** - บันทึก error ลงไฟล์ txt ในโฟลเดอร์ logs

## 📁 Error Logging

ระบบจะบันทึก error logs ทั้งหมดลงไฟล์ txt ในโฟลเดอร์ `logs/`:

### โครงสร้างไฟล์ log:
```
logs/
├── error_2026-02-02.txt
├── error_2026-02-03.txt
└── error_2026-02-04.txt
```

### ข้อมูลที่บันทึก:
- Database connection errors
- API request/response errors
- Temperature polling errors
- Device communication errors
- Legacy API notification errors

### ตัวอย่าง log entry:
```
[ERROR] 2026/02/02 10:30:45 logger.go:54: pollAndSave - Failed to load devices: connection refused
[ERROR] 2026/02/02 10:35:12 logger.go:54: GetDevices failed: database connection lost
[ERROR] 2026/02/02 11:20:33 logger.go:54: API Notification - Failed to send to http://api.example.com: timeout
```

### การจัดการไฟล์ log:
- ไฟล์ log จะถูกสร้างใหม่ทุกวัน (ตามวันที่)
- Error จะถูกเขียนลงไฟล์พร้อมกับแสดงใน console
- โฟลเดอร์ `logs/` จะถูกสร้างอัตโนมัติ
- ไฟล์ log เก่าจะไม่ถูกลบอัตโนมัติ (ต้องลบด้วยตนเอง)

### ดู log file:
**Windows:**
```cmd
cd C:\TMS\logs
type error_2026-02-02.txt
```

**Mac/Linux:**
```bash
cd logs
cat error_2026-02-02.txt
```

## 📝 สรุป

| Feature | Description |
|---------|-------------|
| **Console Mode** | ✅ แสดง logs ใน console window |
| **Stop Method** | ปิด window หรือ Ctrl+C |
| **Logs** | Real-time logs ใน console |
| **Background** | ❌ ต้องมี console window |
| **Size** | ~8.6 MB |

---

**Updated:** February 2, 2026  
**Version:** Console Application (Simple Mode)
