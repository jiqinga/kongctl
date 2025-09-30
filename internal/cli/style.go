package cli

import (
    "fmt"
    "os"
    "strings"

    "github.com/spf13/viper"
    "github.com/spf13/cobra"
)

func useColor() bool {
    if viper.GetBool("no_color") { return false }
    if strings.ToLower(os.Getenv("NO_COLOR")) != "" { return false }
    return true
}

func colorize(s, code string) string {
    if !useColor() { return s }
    return code + s + "\033[0m"
}

func colorSuccess(s string) string { return colorize(s, "\033[32m") }
func colorInfo(s string) string    { return colorize(s, "\033[36m") }
func colorWarn(s string) string    { return colorize(s, "\033[33m") }
func colorError(s string) string   { return colorize(s, "\033[31;1m") }

const (
    emojiSuccess = "✅"
    emojiInfo    = "ℹ️"
    emojiWarn    = "⚠️"
    emojiError   = "❌"
)

func PrintSuccess(cmd *cobra.Command, format string, args ...any) {
    msg := fmt.Sprintf(format, args...)
    cmd.Println(colorSuccess(emojiSuccess + " " + msg))
}

func PrintInfo(cmd *cobra.Command, format string, args ...any) {
    msg := fmt.Sprintf(format, args...)
    cmd.Println(colorInfo(emojiInfo + " " + msg))
}

func PrintWarn(cmd *cobra.Command, format string, args ...any) {
    msg := fmt.Sprintf(format, args...)
    cmd.Println(colorWarn(emojiWarn + " " + msg))
}

func ErrorMessage(s string) string {
    return colorError(emojiError + " " + s)
}

