package model

// LineProfile stores the simplified line-management view built on top of
// 3x-ui's native inbound and Xray configuration model.
type LineProfile struct {
	Id          int    `json:"id" gorm:"primaryKey;autoIncrement"`
	UserId      int    `json:"userId" gorm:"index"`
	Name        string `json:"name"`
	Type        string `json:"type" gorm:"index"`
	Status      string `json:"status" gorm:"index;default:draft"`
	InboundId   *int   `json:"inboundId,omitempty" gorm:"index"`
	OutboundTag string `json:"outboundTag"`
	EntryHost   string `json:"entryHost"`
	EntryPort   int    `json:"entryPort"`
	ChainText   string `json:"chainText"`
	ConfigJSON  string `json:"-"` // Internal configuration can include a Reality private key.
	LastCheckAt int64  `json:"lastCheckAt" gorm:"default:0"`
	LastError   string `json:"lastError"`
	CreatedAt   int64  `json:"createdAt" gorm:"autoCreateTime"`
	UpdatedAt   int64  `json:"updatedAt" gorm:"autoUpdateTime"`
}

// LineOutbound stores the residential landing proxy attached to a line.
type LineOutbound struct {
	Id        int    `json:"id" gorm:"primaryKey;autoIncrement"`
	LineId    int    `json:"lineId" gorm:"index"`
	Type      string `json:"type"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	Username  string `json:"username"`
	Password  string `json:"-"` // write-only; never return secrets to the browser
	Tag       string `json:"tag" gorm:"index"`
	Enabled   bool   `json:"enabled" gorm:"default:true"`
	CreatedAt int64  `json:"createdAt" gorm:"autoCreateTime"`
	UpdatedAt int64  `json:"updatedAt" gorm:"autoUpdateTime"`
}

// LineApplyLog records apply/check/rollback operations for a line.
type LineApplyLog struct {
	Id        int    `json:"id" gorm:"primaryKey;autoIncrement"`
	LineId    int    `json:"lineId" gorm:"index"`
	Action    string `json:"action" gorm:"index"`
	Level     string `json:"level" gorm:"index"`
	Message   string `json:"message"`
	Detail    string `json:"detail"`
	CreatedAt int64  `json:"createdAt" gorm:"autoCreateTime"`
}

// LineCheckResult stores the latest structured health check output.
type LineCheckResult struct {
	Id        int    `json:"id" gorm:"primaryKey;autoIncrement"`
	LineId    int    `json:"lineId" gorm:"index"`
	Status    string `json:"status" gorm:"index"`
	PassCount int    `json:"passCount"`
	WarnCount int    `json:"warnCount"`
	FailCount int    `json:"failCount"`
	ItemsJSON string `json:"itemsJson"`
	CreatedAt int64  `json:"createdAt" gorm:"autoCreateTime"`
}
