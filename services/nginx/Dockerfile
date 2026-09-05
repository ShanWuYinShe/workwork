ARG NGINX_VERSION=1.30.4

FROM nginx:${NGINX_VERSION}-alpine

RUN sed -i 's#dl-cdn.alpinelinux.org#mirrors.aliyun.com#g' /etc/apk/repositories \
    && apk add --no-cache fail2ban supervisor curl logrotate

# Cloudflare Authenticated Origin Pulls — shared public CA certificate
# curl -fsSL 已保证 HTTPS 传输,这里再校验 PEM 结构,防止任何错误页被误存为证书
# (证书内容由 CF 官方发布,sha256 固定会在证书轮换时误伤构建,故校验格式而非哈希)。
RUN curl -fsSL -o /etc/nginx/cloudflare-origin-pull.pem \
        https://developers.cloudflare.com/ssl/static/authenticated_origin_pull_ca.pem \
    && grep -q -- "-----BEGIN CERTIFICATE-----" /etc/nginx/cloudflare-origin-pull.pem \
    && grep -q -- "-----END CERTIFICATE-----" /etc/nginx/cloudflare-origin-pull.pem

# Cloudflare IP range fetch script (+ scheduled refresh wrapper)
COPY fetch-cloudflare-ips.sh /usr/local/bin/fetch-cloudflare-ips.sh
COPY refresh-cloudflare-ips.sh /usr/local/bin/refresh-cloudflare-ips.sh
RUN chmod +x /usr/local/bin/fetch-cloudflare-ips.sh /usr/local/bin/refresh-cloudflare-ips.sh

COPY config/nginx-main.conf /etc/nginx/nginx.conf
COPY config/logrotate.conf /etc/logrotate.d/nginx
COPY config/fail2ban/fail2ban.local /etc/fail2ban/fail2ban.local
COPY config/fail2ban/jail.local /etc/fail2ban/jail.local
COPY config/fail2ban/filter.d/nginx-auth.conf /etc/fail2ban/filter.d/nginx-auth.conf
COPY config/fail2ban/action.d/admin-api.conf /etc/fail2ban/action.d/admin-api.conf
COPY config/supervisord.conf /etc/supervisor/conf.d/supervisord.conf

VOLUME ["/var/log/nginx", "/var/lib/fail2ban/data"]

COPY docker-entrypoint.sh /docker-entrypoint.sh
RUN chmod +x /docker-entrypoint.sh
ENTRYPOINT ["/docker-entrypoint.sh"]
CMD ["supervisord", "-c", "/etc/supervisor/conf.d/supervisord.conf"]
