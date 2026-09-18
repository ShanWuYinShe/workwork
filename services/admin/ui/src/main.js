// @ts-check
import { mount } from 'svelte';
import './app.css';
import App from './App.svelte';

const target = document.getElementById('app');
if (!target) {
  throw new Error('#app 挂载点缺失：index.html 被改动？');
}

const app = mount(App, {
  target,
});

export default app;
