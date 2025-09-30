package config

import "github.com/spf13/viper"

// Admin 配置视图（便于在内部模块传递）
type Admin struct {
    AdminURL      string
    Token         string
    Workspace     string
    TLSSkipVerify bool
}

func FromViper() Admin {
    return Admin{
        AdminURL:      viper.GetString("admin_url"),
        Token:         viper.GetString("token"),
        Workspace:     viper.GetString("workspace"),
        TLSSkipVerify: viper.GetBool("tls_skip_verify"),
    }
}

