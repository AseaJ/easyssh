package deploy

import (
	"context"
	"os/exec"
)

// CommandRunner 抽象命令执行,便于测试注入 mock。
type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) error
}

// ExecRunner 使用 os/exec 执行命令。
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.Run()
}

// runParts 把一条 shell 命令行拆成 (name, args)。
// 支持 Windows 的 "cmd /c ..." 与 Unix 的 "sh -c ..." 由调用方决定。
func runParts(command string) (name string, args []string) {
	// 简单拆分:按空白切分第一个 token 为程序,其余为参数
	var tokens []string
	cur := ""
	inQuote := false
	for _, r := range command {
		switch {
		case r == '"':
			inQuote = !inQuote
		case r == ' ' || r == '\t':
			if !inQuote && cur != "" {
				tokens = append(tokens, cur)
				cur = ""
			} else {
				cur += string(r)
			}
		default:
			cur += string(r)
		}
	}
	if cur != "" {
		tokens = append(tokens, cur)
	}
	if len(tokens) == 0 {
		return "", nil
	}
	return tokens[0], tokens[1:]
}
