FROM nginx:1.30-alpine

RUN apk add --no-cache fail2ban iptables supervisor

COPY config/fail2ban/jail.local /etc/fail2ban/jail.local
COPY config/fail2ban/filter.d/nginx-auth.conf /etc/fail2ban/filter.d/nginx-auth.conf
COPY config/fail2ban/action.d/iptables-allports.conf /etc/fail2ban/action.d/iptables-allports.conf
COPY config/supervisord.conf /etc/supervisor/conf.d/supervisord.conf

VOLUME ["/var/log/nginx", "/var/lib/fail2ban/data"]

CMD ["supervisord", "-c", "/etc/supervisor/conf.d/supervisord.conf"]
