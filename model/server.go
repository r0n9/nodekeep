package model

import (
	"encoding/json"
	"fmt"
	"html/template"
	"time"
)

type Server struct {
	Common
	Name         string
	Tag          string // 分组名
	Secret       string `gorm:"uniqueIndex" json:"-"`
	Note         string `json:"-"` // 管理员可见备注
	DisplayIndex int    // 展示排序，越大越靠前
}

type ServerRuntime struct {
	Server
	Host       *Host      `gorm:"-"`
	State      *HostState `gorm:"-"`
	LastActive time.Time  `gorm:"-"`
}

type PublicHost struct {
	Platform        string
	PlatformVersion string
	CPU             []string
	MemTotal        uint64
	DiskTotal       uint64
	SwapTotal       uint64
	Arch            string
	Virtualization  string
	BootTime        uint64
	CountryCode     string
	Version         string
}

type PublicServerRuntime struct {
	ID         uint64
	Name       string
	Tag        string
	Host       *PublicHost
	State      *HostState
	LastActive time.Time
}

func (s Server) Marshal() template.JS {
	name, _ := json.Marshal(s.Name)
	tag, _ := json.Marshal(s.Tag)
	note, _ := json.Marshal(s.Note)
	secret, _ := json.Marshal(s.Secret)
	return template.JS(fmt.Sprintf(`{"ID":%d,"Name":%s,"Secret":%s,"DisplayIndex":%d,"Tag":%s,"Note":%s}`,
		s.ID, name, secret, s.DisplayIndex, tag, note))
}
