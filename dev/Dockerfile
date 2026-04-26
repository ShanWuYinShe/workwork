# ===========================================
# dev-env Development Container
# Version: 4.2.0
# ===========================================

# Stage 1: Extract Go toolchain
FROM node:22-alpine AS go-source

RUN apk add --no-cache go

# Stage 2: Final image
FROM node:22-alpine

RUN apk add --no-cache \
    git \
    curl \
    bash \
    fish \
    tzdata \
    jq \
    vim \
    wget \
    less \
    postgresql-client \
    redis \
    && rm -rf /var/cache/apk/*

COPY --from=go-source /usr/bin/go /usr/bin/go
COPY --from=go-source /usr/lib/go /usr/lib/go

RUN mkdir -p /root/.config/fish \
    /root/go/bin \
    /root/.npm-global \
    /workspace

RUN echo 'set -gx GOPROXY https://goproxy.cn,direct' > /root/.config/fish/config.fish \
    && echo 'set -gx GOPATH /root/go' >> /root/.config/fish/config.fish \
    && echo 'set -gx PATH $PATH /root/go/bin /root/.npm-global/bin' >> /root/.config/fish/config.fish \
    && echo 'set -gx EDITOR vim' >> /root/.config/fish/config.fish \
    && echo 'set -gx VISUAL vim' >> /root/.config/fish/config.fish \
    && echo 'set -gx PAGER less' >> /root/.config/fish/config.fish

RUN echo '' >> /root/.config/fish/config.fish \
    && echo '# Shortcuts' >> /root/.config/fish/config.fish \
    && echo 'alias pg="PGPASSWORD=$POSTGRES_PASSWORD psql -h $POSTGRES_HOST -U $POSTGRES_USER -d $POSTGRES_DB"' >> /root/.config/fish/config.fish \
    && echo 'alias rd="redis-cli -h $REDIS_HOST"' >> /root/.config/fish/config.fish \
    && echo 'alias ll="ls -la"' >> /root/.config/fish/config.fish \
    && echo 'alias ws="cd /workspace"' >> /root/.config/fish/config.fish \
    && echo 'alias bn=bun' >> /root/.config/fish/config.fish

RUN npm install -g typescript ts-node pnpm bun && corepack enable yarn

WORKDIR /workspace

ENV GOROOT=/usr/lib/go
ENV GOPROXY=https://goproxy.cn,direct
ENV GOPATH=/root/go
ENV PATH="/usr/local/bin:/usr/lib/go/bin:/root/go/bin:/root/.npm-global/bin:${PATH}"
ENV EDITOR=vim

LABEL maintainer="dev-env" \
      version="4.2.0" \
      description="Development Environment Container" \
      dev.env.version="4.2.0"

# Clear upstream node entrypoint — our CMD should run directly
ENTRYPOINT []
CMD ["fish"]
