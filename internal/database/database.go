package database

import (
	"fmt"
	"log"
	"os"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var DB *gorm.DB

func Connect() error {
	host := os.Getenv("DB_HOST")
	user := os.Getenv("DB_USER")
	password := os.Getenv("DB_PASSWORD")
	dbname := os.Getenv("DB_NAME")
	port := os.Getenv("DB_PORT")

	if port == "" {
		port = "3306"
	}

	// Get charset from environment or default to database's charset
	charset := os.Getenv("DB_CHARSET")
	if charset == "" {
		// Default to tis620 for Thai database compatibility
		// Change to utf8mb4 if your database uses utf8mb4
		charset = "tis620"
	}

	// Build DSN with explicit charset to avoid mismatch issues
	// parseTime=True: Parse datetime to time.Time
	// loc=Local: Use local timezone
	// charset: Set connection charset
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=True&loc=Local&charset=%s",
		user, password, host, port, dbname, charset)

	log.Printf("Connecting to database with charset: %s", charset)
	log.Printf("DSN (without password): %s:***@tcp(%s:%s)/%s?parseTime=True&loc=Local&charset=%s",
		user, host, port, dbname, charset)

	var err error
	DB, err = gorm.Open(mysql.New(mysql.Config{
		DSN:                       dsn,
		SkipInitializeWithVersion: false,
		// Disable RETURNING clause which MariaDB doesn't support
		DisableWithReturning: true,
		// Don't reject zero dates, let them pass through
		DontSupportRenameIndex:  true,
		DontSupportRenameColumn: true,
	}), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info),
		// Don't add default timestamp values
		NowFunc: func() time.Time {
			return GetThailandTime()
		},
	})

	if err != nil {
		// Don't use utils.LogError here to avoid import cycle
		// Error logging will be handled by the caller
		return fmt.Errorf("failed to connect to database (check charset compatibility): %w", err)
	}

	// Test the connection
	sqlDB, err := DB.DB()
	if err != nil {
		return fmt.Errorf("failed to get database instance: %w", err)
	}

	// Ping database to ensure connection is alive
	if err := sqlDB.Ping(); err != nil {
		return fmt.Errorf("failed to ping database (charset mismatch may cause issues): %w", err)
	}

	// Set charset on the connection (additional safety)
	if charset == "tis620" {
		// For TIS-620 Thai charset
		if err := DB.Exec("SET NAMES 'tis620' COLLATE 'tis620_thai_ci'").Error; err != nil {
			log.Printf("⚠️  Warning: Could not set TIS-620 charset: %v", err)
		} else {
			log.Println("✅ Database charset set to TIS-620 (tis620_thai_ci)")
		}
	} else if charset == "utf8mb4" {
		// For UTF-8 charset
		if err := DB.Exec("SET NAMES 'utf8mb4' COLLATE 'utf8mb4_unicode_ci'").Error; err != nil {
			log.Printf("⚠️  Warning: Could not set UTF-8MB4 charset: %v", err)
		} else {
			log.Println("✅ Database charset set to UTF-8MB4 (utf8mb4_unicode_ci)")
		}
	}

	sqlDB, err = DB.DB()
	if err != nil {
		// Don't use utils.LogError here to avoid import cycle
		return fmt.Errorf("failed to get database instance: %w", err)
	}

	// Connection pool settings
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetMaxOpenConns(100)
	sqlDB.SetConnMaxLifetime(time.Hour)

	log.Println("Database connected successfully")
	return nil
}

// GetThailandTime returns current time in Thailand timezone (+7)
func GetThailandTime() time.Time {
	// Try to load Asia/Bangkok timezone
	loc, err := time.LoadLocation("Asia/Bangkok")
	if err != nil {
		// Fallback: Use fixed offset UTC+7 for Thailand
		// This works on Windows where timezone database might not be available
		loc = time.FixedZone("Thailand", 7*60*60)
	}
	return time.Now().In(loc)
}
