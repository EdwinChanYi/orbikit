// Package deps pins third-party modules used by services that are still being migrated.
package deps

import (
	_ "github.com/gorilla/websocket"
	_ "github.com/panjf2000/ants/v2"
	_ "github.com/xxtea/xxtea-go/xxtea"
	_ "golang.org/x/time/rate"
)
