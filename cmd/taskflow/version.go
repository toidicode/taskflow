package main

// version là phiên bản của TaskFlow. Đây là biến chứ không phải hằng số,
// để lúc build gán được giá trị khác bằng -ldflags "-X main.version=...".
var version = "dev"
