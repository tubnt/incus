package portal

import (
	"net/http"

	"github.com/incuscloud/incus-admin/internal/httpx"
)

// isValidName 保留包级入口，复用 httpx 中的共享实现。避免各 handler 文件到处
// 改 import；函数体极短，开销可忽略。
func isValidName(s string) bool { return httpx.IsValidName(s) }

// decodeAndValidate 同样是薄包装：portal 包内 handler 继续沿用原函数名，
// 真正的解析 + 校验逻辑定义在 internal/httpx 共享 pkg。
func decodeAndValidate(w http.ResponseWriter, r *http.Request, dst any) bool {
	return httpx.DecodeAndValidate(w, r, dst)
}
