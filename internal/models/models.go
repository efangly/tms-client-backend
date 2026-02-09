package models

import (
	"time"

	"gorm.io/gorm"
)

// MasterMachine represents the master_machine table (main device/probe config)
type MasterMachine struct {
	MachineIP   string   `gorm:"column:machine_ip;size:20;primaryKey" json:"machineIp"`
	ProbeNo     int      `gorm:"column:probe_no;primaryKey;default:1" json:"probeNo"`
	ProbeAll    int      `gorm:"column:probe_all;default:1" json:"probeAll"`
	MachineName string   `gorm:"column:machine_name;size:50" json:"machineName"`
	Color       string   `gorm:"column:color;size:20;default:'000000'" json:"color"`
	ChkOnline   string   `gorm:"column:chkOnline;size:1;default:'0'" json:"chkOnline"`
	ChkSms      string   `gorm:"column:chkSms;size:1;default:'0'" json:"chkSms"`
	ChkMail     string   `gorm:"column:chkMail;size:1;default:'0'" json:"chkMail"`
	ChkMon      string   `gorm:"column:chkMon;size:1;default:'0'" json:"chkMon"`
	ChkLine     string   `gorm:"column:chkLine;size:1;default:'0'" json:"chkLine"`
	ChkReport   string   `gorm:"column:chkReport;size:1;default:'0'" json:"chkReport"`
	MinTemp     *float64 `gorm:"column:min_temp" json:"minTemp"`
	MaxTemp     *float64 `gorm:"column:max_temp" json:"maxTemp"`
	AdjTemp     *float64 `gorm:"column:adj_temp;default:0" json:"adjTemp"`
	SType       string   `gorm:"column:sType;size:1;default:'t'" json:"sType"` // t=Temp, h=Humidity, p=Power
	Port        int      `gorm:"-" json:"port"`                                // Not in DB, set from config
}

// TableName specifies table name for MasterMachine
func (MasterMachine) TableName() string {
	return "master_machine"
}

// GetMinTemp returns min_temp with default value
func (m *MasterMachine) GetMinTemp() float64 {
	if m.MinTemp != nil {
		return *m.MinTemp
	}
	return 0
}

// GetMaxTemp returns max_temp with default value
func (m *MasterMachine) GetMaxTemp() float64 {
	if m.MaxTemp != nil {
		return *m.MaxTemp
	}
	return 100
}

// GetAdjTemp returns adj_temp with default value
func (m *MasterMachine) GetAdjTemp() float64 {
	if m.AdjTemp != nil {
		return *m.AdjTemp
	}
	return 0
}

// IsTemperatureType returns true if sType is 't' (temperature)
func (m *MasterMachine) IsTemperatureType() bool {
	return m.SType == "t" || m.SType == ""
}

// IsHumidityType returns true if sType is 'h' (humidity)
func (m *MasterMachine) IsHumidityType() bool {
	return m.SType == "h"
}

// IsPowerType returns true if sType is 'p' (power)
func (m *MasterMachine) IsPowerType() bool {
	return m.SType == "p"
}

// GetTypeLabel returns human-readable type label
func (m *MasterMachine) GetTypeLabel() string {
	switch m.SType {
	case "h":
		return "Humidity"
	case "p":
		return "Power"
	default:
		return "Temperature"
	}
}

// GetUnit returns the unit for this sensor type
func (m *MasterMachine) GetUnit() string {
	switch m.SType {
	case "h":
		return "%"
	case "p":
		return "W"
	default:
		return "°C"
	}
}

// TempLog represents the temp_log table
type TempLog struct {
	MachineIP  string     `gorm:"column:machine_ip;size:15;primaryKey" json:"machineIp"`
	ProbeNo    int        `gorm:"column:probe_no;primaryKey;default:1" json:"probeNo"`
	McuID      *string    `gorm:"column:mcu_id;size:2" json:"mcuId"`
	TempValue  *float64   `gorm:"column:temp_value" json:"tempValue"`
	RealValue  *int       `gorm:"column:real_value" json:"realValue"`
	Status     *string    `gorm:"column:status;size:8" json:"status"`
	SendTime   *time.Time `gorm:"column:send_time" json:"sendTime"`
	InsertTime time.Time  `gorm:"column:insert_time;primaryKey;type:datetime" json:"insertTime"`
	SDate      *string    `gorm:"column:sDate;size:8" json:"sDate"`
	STime      *string    `gorm:"column:sTime;size:2" json:"sTime"`
}

// BeforeCreate is a GORM hook that runs before creating a record
func (t *TempLog) BeforeCreate(tx *gorm.DB) error {
	// Ensure InsertTime is set and format it explicitly
	if t.InsertTime.IsZero() {
		t.InsertTime = time.Now()
	}
	// Force GORM to use the exact timestamp value
	tx.Statement.SetColumn("insert_time", t.InsertTime.Format("2006-01-02 15:04:05.000"))
	return nil
}

// TableName specifies table name for TempLog
func (TempLog) TableName() string {
	return "temp_log"
}

// TempError represents the temp_error table
type TempError struct {
	MachineIP      string     `gorm:"column:machine_ip;size:15;primaryKey" json:"machineIp"`
	ProbeNo        int        `gorm:"column:probe_no;primaryKey" json:"probeNo"`
	MachineName    *string    `gorm:"column:machine_name;size:50" json:"machineName"`
	TempValue      *float64   `gorm:"column:temp_value" json:"tempValue"`
	ErrorTime      time.Time  `gorm:"column:error_time;primaryKey;type:datetime" json:"errorTime"`
	SmsStatus      int        `gorm:"column:sms_status;default:0" json:"smsStatus"`
	SmsSendTime    *time.Time `gorm:"column:sms_send_time" json:"smsSendTime"`
	SmsSendStatus  int        `gorm:"column:sms_send_status;default:0" json:"smsSendStatus"`
	MailStatus     int        `gorm:"column:mail_status;default:0" json:"mailStatus"`
	MailSendTime   *time.Time `gorm:"column:mail_send_time" json:"mailSendTime"`
	MailSendStatus int        `gorm:"column:mail_send_status;default:0" json:"mailSendStatus"`
	LineStatus     int        `gorm:"column:line_status;default:0" json:"lineStatus"`
	LineSendTime   *time.Time `gorm:"column:line_send_time" json:"lineSendTime"`
	LineSendStatus int        `gorm:"column:line_send_status;default:0" json:"lineSendStatus"`
	MinTemp        *float64   `gorm:"column:min_temp" json:"minTemp"`
	MaxTemp        *float64   `gorm:"column:max_temp" json:"maxTemp"`
	MonStatus      int        `gorm:"column:mon_status;default:0" json:"monStatus"`
	MailCount      int        `gorm:"column:mail_count;default:0" json:"mailCount"`
	SmsCount       int        `gorm:"column:sms_count;default:0" json:"smsCount"`
	LineCount      int        `gorm:"column:line_count;default:0" json:"lineCount"`
	SType          string     `gorm:"column:sType;size:1;default:'t'" json:"sType"`
	TempStatus     string     `gorm:"column:temp_status;size:1;default:'p'" json:"tempStatus"` // p=process, f=finish
	ErrorType      string     `gorm:"column:error_type;size:1;default:'o'" json:"errorType"`   // o=Over, n=Normal
}

// TableName specifies table name for TempError
func (TempError) TableName() string {
	return "temp_error"
}

// BeforeCreate is a GORM hook for TempError
func (t *TempError) BeforeCreate(tx *gorm.DB) error {
	// Ensure ErrorTime is set and format it explicitly
	if t.ErrorTime.IsZero() {
		t.ErrorTime = time.Now()
	}
	// Force GORM to use the exact timestamp value
	tx.Statement.SetColumn("error_time", t.ErrorTime.Format("2006-01-02 15:04:05.000"))
	return nil
}

// ConfigValue represents the config_value table
type ConfigValue struct {
	ID          int     `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	ConfigKey   string  `gorm:"column:config_key;size:255;uniqueIndex" json:"configKey"`
	ConfigValue *string `gorm:"column:config_value;type:text" json:"configValue"`
}

// TableName specifies table name for ConfigValue
func (ConfigValue) TableName() string {
	return "config_value"
}

// MasterUser represents the master_user table
type MasterUser struct {
	ID       int     `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	Username string  `gorm:"column:username;size:255;uniqueIndex" json:"username"`
	Password string  `gorm:"column:password;size:255" json:"-"`
	Fullname *string `gorm:"column:fullname;size:255" json:"fullname"`
	Role     *string `gorm:"column:role;size:50" json:"role"`
}

// TableName specifies table name for MasterUser
func (MasterUser) TableName() string {
	return "master_user"
}

// ========== Response/DTO Structures ==========

// MachineWithStatus represents a machine with its current status (for API response)
type MachineWithStatus struct {
	MasterMachine
	CurrentValue *float64 `json:"currentValue"`
	LastUpdate   *string  `json:"lastUpdate"`
	OnlineStatus string   `json:"onlineStatus"`
}

// DeviceGroup represents a group of probes for the same IP (for API response)
type DeviceGroup struct {
	MachineIP    string              `json:"machineIp"`
	MachineName  string              `json:"machineName"`
	ProbeCount   int                 `json:"probeCount"`
	OnlineStatus string              `json:"onlineStatus"`
	Probes       []MachineWithStatus `json:"probes"`
}
