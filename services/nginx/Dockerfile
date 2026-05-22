FROM nginx:stable-alpine

RUN apk add --no-cache fail2ban supervisor curl logrotate apache2-utils

# Cloudflare Authenticated Origin Pulls — shared public CA certificate
RUN curl -sS -o /etc/nginx/cloudflare-origin-pull.pem \
    https://developers.cloudflare.com/ssl/static/authenticated_origin_pull_ca.pem

# Cloudflare IP range fetch script
COPY fetch-cloudflare-ips.sh /usr/local/bin/fetch-cloudflare-ips.sh
RUN chmod +x /usr/local/bin/fetch-cloudflare-ips.sh

COPY config/nginx-main.conf /etc/nginx/nginx.conf
COPY config/logrotate.conf /etc/logrotate.d/nginx
COPY config/fail2ban/jail.local /etc/fail2ban/jail.local
COPY config/fail2ban/filter.d/nginx-auth.conf /etc/fail2ban/filter.d/nginx-auth.conf
COPY config/fail2ban/action.d/admin-api.conf /etc/fail2ban/action.d/admin-api.conf
COPY config/supervisord.conf /etc/supervisor/conf.d/supervisord.conf

VOLUME ["/var/log/nginx", "/var/lib/fail2ban/data"]

COPY docker-entrypoint.sh /docker-entrypoint.sh
RUN chmod +x /docker-entrypoint.sh
ENTRYPOINT ["/docker-entrypoint.sh"]
CMD ["supervisord", "-c", "/etc/supervisor/conf.d/supervisord.conf"]
