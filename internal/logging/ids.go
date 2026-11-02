package logging

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
)

// HashID 用于日志中标识 Telegram 用户/聊天，避免直接输出原始 ID。
func HashID(id int64) string {
	sum := sha256.Sum256([]byte(strconv.FormatInt(id, 10)))
	return hex.EncodeToString(sum[:])[:12]
}
