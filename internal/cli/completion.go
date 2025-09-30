package cli

import (
    "fmt"
    "os"

    "github.com/spf13/cobra"
)

var completionCmd = &cobra.Command{
    Use:                   "completion [bash|zsh|fish|powershell]",
    Short:                 "生成 Shell 自动补全脚本",
    Long: `为 bash/zsh/fish/PowerShell 生成自动补全脚本并输出到标准输出。

临时启用（当前会话）示例：
- Bash:        source <(kongctl completion bash)
- Zsh:         source <(kongctl completion zsh)
- Fish:        kongctl completion fish | source
- PowerShell:  kongctl completion powershell | Out-String | Invoke-Expression

持久安装（示例，按需调整路径）：
- Bash(Linux):        kongctl completion bash | sudo tee /etc/bash_completion.d/kongctl > /dev/null
- Bash(macOS Homebrew): kongctl completion bash > $(brew --prefix)/etc/bash_completion.d/kongctl
- Zsh:                kongctl completion zsh > ${fpath[1]}/_kongctl  或将输出重定向到 ~/.zsh/completions/_kongctl 并确保在 fpath 中
- Fish:               kongctl completion fish > ~/.config/fish/completions/kongctl.fish
- PowerShell:         kongctl completion powershell > $PROFILE\n添加行 . $PROFILE 以在会话中加载，或按需使用 Microsoft 文档的持久方案`,
    Example: `# Bash（临时生效）
source <(kongctl completion bash)

# Zsh（临时生效）
source <(kongctl completion zsh)

# Fish（临时生效）
kongctl completion fish | source

# PowerShell（临时生效）
kongctl completion powershell | Out-String | Invoke-Expression`,
    DisableFlagsInUseLine: true,
    Args:                  cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
    ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
    RunE: func(cmd *cobra.Command, args []string) error {
        shell := args[0]
        switch shell {
        case "bash":
            return rootCmd.GenBashCompletionV2(os.Stdout, true)
        case "zsh":
            return rootCmd.GenZshCompletion(os.Stdout)
        case "fish":
            return rootCmd.GenFishCompletion(os.Stdout, true)
        case "powershell":
            return rootCmd.GenPowerShellCompletionWithDesc(os.Stdout)
        default:
            return fmt.Errorf("未知 shell：%s（支持 bash/zsh/fish/powershell）", shell)
        }
    },
}

