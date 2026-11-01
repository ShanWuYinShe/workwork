package requestctx

import "context"

// Actor 是认证中间件写入请求上下文的身份。领域包只依赖本类型，不依赖
// auth/identity 的实现；actor 为空表示请求未认证（公开端点）。
type Actor struct {
	Username  string
	Role      string
	CreatedAt string
	// Internal 表示 internal token / fail2ban token 伪身份。
	Internal bool
}

type key struct{}

func WithActor(ctx context.Context, actor Actor) context.Context {
	return context.WithValue(ctx, key{}, actor)
}

func ActorFrom(ctx context.Context) (Actor, bool) {
	actor, ok := ctx.Value(key{}).(Actor)
	return actor, ok
}

func Username(ctx context.Context) string {
	actor, _ := ActorFrom(ctx)
	return actor.Username
}
