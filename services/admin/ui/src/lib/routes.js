import Dashboard from '../routes/Dashboard.svelte';
import Services from '../routes/Services.svelte';
import Clients from '../routes/Clients.svelte';
import Users from '../routes/Users.svelte';
import Security from '../routes/Security.svelte';
import Settings from '../routes/Settings.svelte';
import Downloads from '../routes/Downloads.svelte';
import Transfer from '../routes/Transfer.svelte';
import Logs from '../routes/Logs.svelte';
import { routeMeta } from './routeMeta.js';

const components = {
  '/': Dashboard,
  '/services': Services,
  '/clients': Clients,
  '/users': Users,
  '/security': Security,
  '/settings': Settings,
  '/downloads': Downloads,
  '/transfer': Transfer,
  '/logs': Logs,
};

export const routes = routeMeta.map(({ path, adminOnly }) => ({
  path,
  component: components[path],
  adminOnly,
}));

export { navSections, routeTitles } from './routeMeta.js';
