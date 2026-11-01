package accounts

import "sync"

// userLifecycleMu 串行化"删除面板用户"与"新增 Xray 客户端"。
// 删除流程写 users.json/htpasswd/xray 台账；在途 add 若在删除完成后写回
// 客户端，会让"删除用户 = 撤销代理身份"不成立。只在 accounts 包内使用。
var userLifecycleMu sync.Mutex
