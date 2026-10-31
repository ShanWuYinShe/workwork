import js from '@eslint/js';
import svelte from 'eslint-plugin-svelte';
import prettier from 'eslint-config-prettier';
import globals from 'globals';

// 门禁取向:错误级规则进 make check/CI。a11y 由 `svelte-check
// --fail-on-warnings` 兜底（eslint-plugin-svelte 3.x 已不再提供 a11y 规则）。
// prettier 管格式,eslint 关闭所有格式类规则避免打架。
export default [
  {
    ignores: ['dist/**', 'node_modules/**', 'vite.config.js'],
  },
  js.configs.recommended,
  ...svelte.configs['flat/recommended'],
  prettier,
  {
    languageOptions: {
      ecmaVersion: 'latest',
      sourceType: 'module',
      globals: { ...globals.browser },
    },
    rules: {
      'no-unused-vars': ['error', { argsIgnorePattern: '^_', varsIgnorePattern: '^_' }],
    },
  },
  {
    files: ['**/*.svelte'],
    rules: {
      // 残留的 svelte-ignore 注释会掩盖真实诊断，按错误处理。
      'svelte/no-unused-svelte-ignore': 'error',
    },
  },
];
