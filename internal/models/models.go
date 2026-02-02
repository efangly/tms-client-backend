package models

import (
	"time"
)

// Device represents the device table
type Device struct {
	ID           string    `gorm:"column:id;primaryKey;size:36" json:"id"`
	Devicename   string    `gorm:"column:devicename;size:255" json:"devicename"`
	IP           string    `gorm:"column:ip;size:255" json:"ip"`
	Port         int       `gorm:"column:port" json:"port"`
	Probe        int       `gorm:"column:probe" json:"probe"`
	Devicetype   string    `gorm:"column:devicetype;size:255" json:"devicetype"`
	Color        string    `gorm:"column:color;size:255" json:"color"`
	Mintemp      float64   `gorm:"column:mintemp" json:"mintemp"`
	Maxtemp      float64   `gorm:"column:maxtemp" json:"maxtemp"`
	Adjtemp      float64   `gorm:"column:adjtemp" json:"adjtemp"`
	SType        string    `gorm:"column:s_type;size:10" json:"sType"`
	Onlinestatus string    `gorm:"column:onlinestatus;size:50" json:"onlinestatus"`
	CreatedAt    time.Time `gorm:"column:created_at" json:"createdAt"`
	UpdatedAt    time.Time `gorm:"column:updated_at" json:"updatedAt"`
}

// TableName specifies table name for Device
func (Device) TableName() string {
	return "device"
}

// TempLog represents the temp_log table
type TempLog struct {
	MachineIP  string     `gorm:"column:machine_ip;size:255;index" json:"machineIp"`
	ProbeNo    int        `gorm:"column:probe_no" json:"probeNo"`
	McuID      *string    `gorm:"column:mcu_id;size:10" json:"mcuId"`
	TempValue  *float64   `gorm:"column:temp_value" json:"tempValue"`
	RealValue  *int       `gorm:"column:real_value" json:"realValue"`
	Status     *string    `gorm:"column:status;size:10" json:"status"`
	SendTime   *time.Time `gorm:"column:send_time" json:"sendTime"`
	InsertTime time.Time  `gorm:"column:insert_time;index" json:"insertTime"`
	SDate      *string    `gorm:"column:sDate;size:10" json:"sDate"`
	STime      *string    `gorm:"column:sTime;size:10" json:"sTime"`
}

// TableName specifies table name for TempLog
func (TempLog) TableName() string {
	return "temp_log"
}

// TempError represents the temp_error table
type TempError struct {
	MachineIP   string    `gorm:"column:machine_ip;size:255" json:"machineIp"`
	ProbeNo     int       `gorm:"column:probe_no" json:"probeNo"`
	MachineName *string   `gorm:"column:machine_name;size:255" json:"machineName"`
	TempValue   *float64  `gorm:"column:temp_value" json:"tempValue"`
	ErrorTime   time.Time `gorm:"column:error_time" json:"errorTime"`
	MinTemp     *float64  `gorm:"column:min_temp" json:"minTemp"`
	MaxTemp     *float64  `gorm:"column:max_temp" json:"maxTemp"`
	TempStatus  *string   `gorm:"column:temp_status;size:10" json:"tempStatus"`
}

// TableName specifies table name for TempError
func (TempError) TableName() string {
	return "temp_error"
}

// MasterMachine represents the master_machine table
type MasterMachine struct {
	ID          int      `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	MachineIP   string   `gorm:"column:machine_ip;size:255;index" json:"machineIp"`
	ProbeNo     int      `gorm:"column:probe_no" json:"probeNo"`
	ProbeAll    int      `gorm:"column:probe_all" json:"probeAll"`
	MachineName string   `gorm:"column:machine_name;size:255" json:"machineName"`
	MinTemp     *float64 `gorm:"column:min_temp" json:"minTemp"`
	MaxTemp     *float64 `gorm:"column:max_temp" json:"maxTemp"`
	AdjTemp     *float64 `gorm:"column:adj_temp" json:"adjTemp"`
}

// TableName specifies table name for MasterMachine
func (MasterMachine) TableName() string {
	return "master_machine"
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
