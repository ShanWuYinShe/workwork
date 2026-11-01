<script>
  import { eyeIcon } from '../eye-icon.js';
  import { t } from '../i18n.svelte.js';

  let {
    id,
    label,
    value = $bindable(''),
    inputClass = 'input-w-md',
    invalid = false,
    describedby = undefined,
  } = $props();

  let show = $state(false);
</script>

<div class="form-group password-field">
  <label for={id}>{label}</label>
  <div class="password-input">
    <input
      {id}
      type={show ? 'text' : 'password'}
      bind:value
      class={inputClass}
      aria-invalid={invalid ? 'true' : undefined}
      aria-describedby={describedby}
    />
    <button
      type="button"
      class="eye-btn"
      onclick={() => (show = !show)}
      aria-label={show ? t('hidePassword') : t('showPassword')}
    >
      <!-- eslint-disable-next-line svelte/no-at-html-tags -- 内容为代码内常量 SVG 映射,非用户输入 -->
      {@html eyeIcon(show)}
    </button>
  </div>
</div>
