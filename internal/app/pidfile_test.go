package app

import (
	"errors"
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
	// 模拟另一个实例:写入当前进程 PID(必定存活)
	os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o644)
	release, err := AcquirePIDFile(path)
	if err == nil {
		t.Fatal("占用时应报错")
	}
	if !errors.Is(err, ErrLocked) {
		t.Errorf("应返回 ErrLocked,实际: %v", err)
	}
	if release != nil {
		t.Error("报错时不应返回 release")
	}
}

// TestAcquirePIDFileStale:残留的死亡 PID 应自动覆盖,不阻止启动。
func TestAcquirePIDFileStale(t *testing.T) {
	path := filepath.Join(t.TempDir(), "go-zs.pid")
	// 写入一个几乎不可能存活的 PID
	if err := os.WriteFile(path, []byte("999999999\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	release, err := AcquirePIDFile(path)
	if err != nil {
		t.Fatalf("残留 PID 应自动覆盖,实际: %v", err)
	}
	data, _ := os.ReadFile(path)
	if strings.TrimSpace(string(data)) != strconv.Itoa(os.Getpid()) {
		t.Errorf("PID 文件应被当前进程覆盖: %q", data)
	}
	release()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("释放后 PID 文件未删除")
	}
}

// TestAcquirePIDFileGarbage:残留的非法内容应被忽略并覆盖。
func TestAcquirePIDFileGarbage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "go-zs.pid")
	if err := os.WriteFile(path, []byte("not-a-pid"), 0o644); err != nil {
		t.Fatal(err)
	}
	release, err := AcquirePIDFile(path)
	if err != nil {
		t.Fatalf("非法内容应被覆盖,实际: %v", err)
	}
	release()
}

func TestAcquirePIDFileEmpty(t *testing.T) {
	release, err := AcquirePIDFile("")
	if err != nil {
		t.Fatal(err)
	}
	release() // 应为空操作
}
