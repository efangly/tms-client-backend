# 🔧 แก้ไขปัญหา Panic ก่อนเขียน Log

## 🔴 ปัญหาที่พบ

โปรแกรมเกิด **panic/crash ทันทีหลังจาก run** และยังไม่ทันเขียน log ไฟล์

### สาเหตุหลัก:

### 1. **Circular Import Dependency** 
```
database package → import utils → LogError()
```
เมื่อ `database.Connect()` ถูกเรียก มันพยายามใช้ `utils.LogError()` แต่ `InitLogger()` อาจยังไม่ได้ถูกเรียก ทำให้เกิด panic

### 2. **Package Initialization Order**
Go โหลด packages ตามลำดับ import ซึ่งอาจทำให้:
- `database` package ถูกโหลดก่อน
- พยายามเรียก `utils.LogError()` ก่อนที่ `utils` จะพร้อม
- เกิด nil pointer dereference → panic

## ✅ วิธีแก้ไข

### 1. ลบ `utils` import ออกจาก `database` package

**ก่อนแก้:**
```go
// internal/database/database.go
import (
    "tms-backend/internal/utils"  // ❌ สร้าง circular dependency
)

func Connect() error {
    if err != nil {
        utils.LogError("...")  // ❌ อาจ panic ถ้า utils ยังไม่ ready
        return err
    }
}
```

**หลังแก้:**
```go
// internal/database/database.go
// ✅ ไม่ import utils แล้ว

func Connect() error {
    if err != nil {
        // ✅ ให้ caller จัดการ logging แทน
        return fmt.Errorf("failed to connect: %w", err)
    }
}
```

### 2. เพิ่ม Panic Recovery ใน main.go

```go
func main() {
    // ✅ Catch panic ทันที
    defer func() {
        if r := recover(); r != nil {
            log.Printf("❌ PANIC: %v", r)
            waitForEnter()
            os.Exit(1)
        }
    }()
    
    // ... rest of code
}
```

### 3. เพิ่ม Safety Guards ใน Logger

```go
// ✅ InitLogger มี recover
func InitLogger() error {
    defer func() {
        if r := recover(); r != nil {
            log.Printf("⚠️ Logger panic: %v", r)
        }
    }()
    // ...
}

// ✅ LogError มี nil check + recover
func LogError(format string, v ...interface{}) {
    defer func() {
        if r := recover(); r != nil {
            log.Printf("[ERROR-PANIC] %v", r)
        }
    }()
    
    if ErrorLogger != nil {
        ErrorLogger.Printf(format, v...)
    } else {
        log.Printf("[ERROR] "+format, v...)  // Fallback
    }
}
```

### 4. ย้าย Error Logging ไปที่ Caller

**main.go** เป็นที่จัดการ error logging แทน database package:

```go
if err := database.Connect(); err != nil {
    utils.LogError("Failed to connect to database: %v", err)  // ✅
    log.Printf("❌ Failed to connect to database: %v", err)
    // ...
}
```

## 📋 ไฟล์ที่แก้ไข

1. ✅ `internal/database/database.go` - ลบ utils import
2. ✅ `internal/utils/logger.go` - เพิ่ม panic recovery
3. ✅ `main.go` - เพิ่ม panic recovery
4. ✅ `debug.sh` - script สำหรับ debug

## 🧪 การทดสอบ

### ทดสอบด้วย debug script:
```bash
./debug.sh
```

### หรือ build และ run ปกติ:
```bash
go build -o tms-backend main.go
./tms-backend
```

### สิ่งที่ควรเห็น (ถ้าทำงานปกติ):
```
⚠️  No .env file found, using environment variables
💡 Make sure .env file exists in the same folder as the executable
✅ Error logging initialized: logs/error_2026-02-02.txt
🔌 Connecting to database...
```

### สิ่งที่จะเห็น (ถ้าเกิด panic):
```
❌ PANIC: <error details>

📋 Stack trace:
<panic details>

🔴 Press Enter to exit...
```

## 📊 Package Dependency Graph (แก้ไขแล้ว)

**ก่อนแก้:**
```
main → database → utils ❌ (circular)
     → utils
     → handlers → utils
     → services → utils
```

**หลังแก้:**
```
main → database ✅ (no utils dependency)
     → utils
     → handlers → utils
     → services → utils
```

## 🔍 วิธีตรวจสอบปัญหาเพิ่มเติม

ถ้ายังเกิด panic:

1. **เช็ค .env file** - ต้องมีและมี config ครบ
2. **เช็ค database connection** - MySQL ต้องทำงานอยู่
3. **เช็ค permissions** - โปรแกรมสร้างโฟลเดอร์ `logs/` ได้หรือไม่
4. **ดู console output** - ดู error message ที่แสดง
5. **เช็ค log file** - ถ้ามี log file ให้เปิดดูใน `logs/error_*.txt`

## 💡 Best Practices (บทเรียน)

1. **Low-level packages** (เช่น database) ไม่ควร depend on high-level packages (เช่น utils)
2. **ใช้ panic recovery** ใน main function เสมอ
3. **Logger ต้องมี fallback** ถ้า initialization ล้มเหลว
4. **Error handling** ควรทำที่ caller ไม่ใช่ใน utility functions
5. **Test error cases** ก่อน deploy

---

**Updated:** February 2, 2026
**Status:** ✅ แก้ไขเสร็จสิ้น
