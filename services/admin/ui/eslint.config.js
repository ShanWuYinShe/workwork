import js from '@eslint/js';
import svelte from 'eslint-plugin-svelte';
import prettier from 'eslint-config-prettier';
import globals from 'globals';

// 门禁取向:错误级规则进 make check/CI,a11y 等存量告警保持 warn(不阻塞但可见,
// 逐条修复后再升级为 error)。prettier 管格式,eslint 关闭所有格式类规则避免打架。
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
      // 存量模板的 a11y 告警(点击 div 无键盘事件等)不作为门禁错误;
      // 新代码应遵循,存量随重构逐条清零后可移除本条降级。
      'svelte/no-unused-svelte-ignore': 'error',
    },
  },
];
