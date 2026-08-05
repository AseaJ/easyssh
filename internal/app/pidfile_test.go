package app

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestAcquirePIDFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "go-zs.pid")
	release, err := AcquirePIDFile(path)
	if err != nil {
		t.Fatalf("首次获取失败: %v", err)
	}
	data, _ := os.ReadFile(path)
	if strings.TrimSpace(string(data)) != strconv.Itoa(os.Getpid()) {
		t.Errorf("PID 文件内容异常: %q", data)
	}
	release()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("释放后 PID 文件未删除")
	}
}

func TestAcquirePIDFileConflict(t *testing.T) {
	path := filepath.Join(t.TempDir(), "go-zs.pid")
	if _, err := AcquirePIDFile(path); err != nil {
		t.Fatal(err)
	}
	// 模拟另一个实例:写入不同 PID
	os.WriteFile(path, []byte("99999"), 0o644)
	if _, err := AcquirePIDFile(path); err == nil {
		t.Fatal("占用时应报错")
	}
}

func TestAcquirePIDFileEmpty(t *testing.T) {
	release, err := AcquirePIDFile("")
	if err != nil {
		t.Fatal(err)
	}
	release() // 应为空操作
}
