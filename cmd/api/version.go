package main

import "runtime/debug"

// version được gán lúc build bằng -ldflags "-X main.version=...", như CLI ở Bài 22.
var version string

// buildVersion trả về version gán lúc build. Không có thì lấy version của module chính mà
// go build ghi vào binary từ git (tag hoặc commit, kèm +dirty nếu còn thay đổi chưa commit).
func buildVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}
