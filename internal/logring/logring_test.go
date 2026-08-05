package logring

import (
	"testing"
)

func TestWriteAndRead(t *testing.T) {
	r := New(10)
	r.Write([]byte("line1\nline2\nline3\n"))
	entries := r.Entries(0)
	if len(entries) != 3 {
		t.Fatalf("条数 = %d,期望 3", len(entries))
	}
	if entries[0].Msg != "line1" || entries[2].Msg != "line3" {
		t.Errorf("内容异常: %v", entries)
	}
}

func TestCapacity(t *testing.T) {
	r := New(2)
	for i := 1; i <= 5; i++ {
		r.Write([]byte("msg" + string(rune('0'+i)) + "\n"))
	}
	entries := r.Entries(0)
	if len(entries) != 2 {
		t.Fatalf("超过容量后条数 = %d,期望 2", len(entries))
	}
	if entries[0].Msg != "msg4" || entries[1].Msg != "msg5" {
		t.Errorf("应保留最新: %v", entries)
	}
}

func TestLimit(t *testing.T) {
	r := New(100)
	r.Write([]byte("a\nb\nc\n"))
	entries := r.Entries(2)
	if len(entries) != 2 || entries[0].Msg != "b" || entries[1].Msg != "c" {
		t.Errorf("limit 截取异常: %v", entries)
	}
}

func TestEmptyWrite(t *testing.T) {
	r := New(10)
	r.Write([]byte(""))
	r.Write([]byte("\n"))
	if r.Len() != 0 {
		t.Errorf("空行不应记录,Len = %d", r.Len())
	}
}
