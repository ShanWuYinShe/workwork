# cliproxyapi + nginx/xray 合并实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 添加 cliproxyapi 服务到 dev-env，合并 nginx/xray 配置

**Architecture:**
- xray 和 nginx 服务重命名为无后缀名称
- cliproxyapi 作为独立服务运行在 ipvlan 网络
- nginx 统一代理 xray 和 cliproxyapi

**Tech Stack:** Docker Compose, xray, nginx, cliproxyapi

---

## File Structure

```
configs/
├── xray-reality.json          # 新建，合并配置
├── nginx-xhttp.conf           # 新建，合并+添加 /workwork
├── cliproxyapi/               # 新建目录
│   ├── config.yaml           # 用户需提供
│   └── auth/                 # auth-dir
dev/
├── Dockerfile.cliproxyapi     # 新建
docker-compose.yml            # 修改，重命名+新增服务
configs/
├── xray-reality-liyou.json   # 删除
├── xray-reality-liusai.json  # 删除
├── nginx-xhttp-liyou.conf    # 删除
├── nginx-xhttp-liusai.conf   # 删除
```

---

## Task 1: 更新 docker-compose.yml

**Files:**
- Modify: `docker-compose.yml`

- [ ] **Step 1: 更新 docker-compose.yml**

```yaml
networks:
  external_net:
    external: true
    name: ipvlan_docker
  internal_net:
    driver: bridge
    internal: true

services:
  xray-reality:
    image: teddysun/xray
    restart: unless-stopped
    networks:
      - external_net
      - internal_net
    volumes:
      - ./configs/xray-reality.json:/etc/xray/config.json:ro

  nginx-xhttp:
    image: nginx:1.30-alpine
    restart: unless-stopped
    networks:
      - external_net
      - internal_net
    volumes:
      - ./cert:/etc/nginx/certs:ro
      - ./configs/nginx-xhttp.conf:/etc/nginx/conf.d/default.conf:ro
    depends_on:
      - xray-reality
      - cliproxyapi

  cliproxyapi:
    build:
      context: ./dev
      dockerfile: Dockerfile.cliproxyapi
    restart: unless-stopped
    networks:
      - internal_net
    volumes:
      - ./configs/cliproxyapi/config.yaml:/CLIProxyAPI/config.yaml:ro
      - ./configs/cliproxyapi/auth:/root/.cli-proxy-api
```

- [ ] **Step 2: 提交**

```bash
git add docker-compose.yml
git commit -m "refactor: rename xray and nginx services, add cliproxyapi service"
```

---

## Task 2: 创建 xray-reality.json

**Files:**
- Create: `configs/xray-reality.json`

- [ ] **Step 1: 创建合并的 xray 配置**

```json
{
  "log": {
    "loglevel": "warning"
  },
  "inbounds": [
    {
      "tag": "xhttp-in",
      "listen": "0.0.0.0",
      "port": 8443,
      "protocol": "vless",
      "settings": {
        "clients": [
          {
            "id": "00fce1b8-cfee-487a-88d4-f9b9208ae2e7",
            "flow": ""
          },
          {
            "id": "96eef15e-cc1a-4536-81e7-7662fb178526",
            "flow": ""
          }
        ],
        "decryption": "none"
      },
      "streamSettings": {
        "network": "xhttp",
        "xhttpSettings": {
          "path": "/proxy/query"
        }
      }
    },
    {
      "tag": "ws-in",
      "listen": "0.0.0.0",
      "port": 8444,
      "protocol": "vless",
      "settings": {
        "clients": [
          {
            "id": "00fce1b8-cfee-487a-88d4-f9b9208ae2e7",
            "flow": ""
          },
          {
            "id": "96eef15e-cc1a-4536-81e7-7662fb178526",
            "flow": ""
          }
        ],
        "decryption": "none"
      },
      "streamSettings": {
        "network": "ws",
        "wsSettings": {
          "path": "/proxy/ws"
        }
      }
    },
    {
      "tag": "reality-in",
      "listen": "::",
      "port": 443,
      "protocol": "vless",
      "settings": {
        "clients": [
          {
            "id": "00fce1b8-cfee-487a-88d4-f9b9208ae2e7",
            "flow": "xtls-rprx-vision"
          },
          {
            "id": "96eef15e-cc1a-4536-81e7-7662fb178526",
            "flow": "xtls-rprx-vision"
          }
        ],
        "decryption": "none"
      },
      "streamSettings": {
        "network": "tcp",
        "security": "reality",
        "realitySettings": {
          "dest": "www.microsoft.com:443",
          "serverNames": ["www.microsoft.com"],
          "privateKey": "0ifuQQy0v6jp1WTMsnQFixWBDv6asLBMzaniY/EWp8g=",
          "shortIds": ["abcdef"]
        }
      }
    }
  ],
  "outbounds": [
    {
      "protocol": "freedom",
      "tag": "direct"
    },
    {
      "protocol": "blackhole",
      "tag": "blocked"
    }
  ]
}
```

- [ ] **Step 2: 提交**

```bash
git add configs/xray-reality.json
git commit -m "feat: merge xray configs with single path /proxy"
```

---

## Task 3: 创建 nginx-xhttp.conf

**Files:**
- Create: `configs/nginx-xhttp.conf`

- [ ] **Step 1: 创建合并的 nginx 配置**

```nginx
# Nginx XHTTP 代理配置
location /proxy/query/ {
    proxy_pass http://xray-reality:8443;
    proxy_http_version 1.1;
    proxy_set_header Connection "";
    proxy_buffering off;
    proxy_cache off;
    proxy_request_buffering off;
    chunked_transfer_encoding on;
    proxy_read_timeout 300s;
    proxy_send_timeout 300s;
}

location /proxy/ws/ {
    proxy_pass http://xray-reality:8444;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_read_timeout 300s;
    proxy_send_timeout 300s;
}

location /workwork/ {
    proxy_pass http://cliproxyapi:8317/;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_read_timeout 300s;
    proxy_send_timeout 300s;
}
```

- [ ] **Step 2: 提交**

```bash
git add configs/nginx-xhttp.conf
git commit -m "feat: merge nginx configs, add /workwork route for cliproxyapi"
```

---

## Task 4: 创建 Dockerfile.cliproxyapi

**Files:**
- Create: `dev/Dockerfile.cliproxyapi`

- [ ] **Step 1: 创建 cliproxyapi Dockerfile**

```dockerfile
FROM alpine:latest

RUN apk add --no-cache curl unzip

RUN curl -fsSL https://raw.githubusercontent.com/brokechubb/cliproxyapi-installer/refs/heads/master/cliproxyapi-installer | sh

WORKDIR /CLIProxyAPI

EXPOSE 8317

CMD ["cli-proxy-api", "run", "--config", "/CLIProxyAPI/config.yaml"]
```

- [ ] **Step 2: 提交**

```bash
git add dev/Dockerfile.cliproxyapi
git commit -m "feat: add cliproxyapi Dockerfile"
```

---

## Task 5: 创建 cliproxyapi 配置目录结构

**Files:**
- Create: `configs/cliproxyapi/config.yaml`
- Create: `configs/cliproxyapi/auth/.gitkeep`

- [ ] **Step 1: 创建默认配置占位文件**

config.yaml 需要用户自行提供，创建占位符：

```yaml
# cliproxyapi configuration
# User needs to provide their own config
```

- [ ] **Step 2: 创建 auth 目录占位**

```bash
touch configs/cliproxyapi/auth/.gitkeep
```

- [ ] **Step 3: 提交**

```bash
git add configs/cliproxyapi/
git commit -m "feat: add cliproxyapi config directory structure"
```

---

## Task 6: 删除旧配置文件

**Files:**
- Delete: `configs/xray-reality-liyou.json`
- Delete: `configs/xray-reality-liusai.json`
- Delete: `configs/nginx-xhttp-liyou.conf`
- Delete: `configs/nginx-xhttp-liusai.conf`

- [ ] **Step 1: 删除旧文件**

```bash
rm configs/xray-reality-liyou.json configs/xray-reality-liusai.json configs/nginx-xhttp-liyou.conf configs/nginx-xhttp-liusai.conf
```

- [ ] **Step 2: 提交**

```bash
git add -u
git commit -m "refactor: remove old xray and nginx config files"
```

---

## Task 7: 更新 .gitignore

**Files:**
- Modify: `.gitignore`

- [ ] **Step 1: 添加 cliproxyapi 目录到 .gitignore**

检查 .gitignore 是否已包含 cliproxyapi 目录，如无则添加：

```
cliproxyapi/
```

- [ ] **Step 2: 提交**

```bash
git add .gitignore
git commit -m "chore: add cliproxyapi to .gitignore"
```

---

## Verification

- [ ] 检查 `docker compose config` 无语法错误
- [ ] 确认用户已提供 `configs/cliproxyapi/config.yaml`
