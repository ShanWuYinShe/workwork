FROM alpine:latest

RUN apk add --no-cache fail2ban iptables python3 py3-pip docker-cli

COPY config/jail.local /etc/fail2ban/jail.local
COPY config/filter.d/*.conf /etc/fail2ban/filter.d/
COPY config/action.d/*.conf /etc/fail2ban/action.d/

RUN chmod 644 /etc/fail2ban/jail.local && \
    chmod 644 /etc/fail2ban/filter.d/*.conf && \
    chmod 755 /etc/fail2ban/action.d/*.conf

VOLUME ["/var/log/nginx", "/var/lib/fail2ban/data"]

CMD ["fail2ban-server", "-f", "-x"]