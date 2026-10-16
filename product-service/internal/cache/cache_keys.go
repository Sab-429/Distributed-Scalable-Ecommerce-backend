package cache

import "fmt"

func ProductKey(id uint) string {
	return fmt.Sprintf("product:%d", id)
}
