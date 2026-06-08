const SERVICE_LABELS = {
  admin: 'Admin',
  media_server: 'Media Server',
  nginx: 'Nginx',
  xray: 'Xray',
};

export function serviceDisplayName(name) {
  return SERVICE_LABELS[name] || name;
}
