package apply

import "fmt"

type Change struct {
    Kind   string // Service/Route/Upstream/Target/Plugin
    Name   string
    Action string // create/update/delete/none
    Diff   string // 人类可读的差异
}

type Plan struct {
    Items []Change
}

func (p Plan) String() string {
    if len(p.Items) == 0 {
        return "无变更"
    }
    s := "变更计划：\n"
    for _, it := range p.Items {
        s += fmt.Sprintf("- [%s] %s => %s\n", it.Kind, it.Name, it.Action)
        if it.Diff != "" {
            s += it.Diff + "\n"
        }
    }
    return s
}

