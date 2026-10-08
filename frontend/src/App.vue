<script setup lang="ts">
import { notice } from "./api";
</script>
<template>
  <RouterView /><Transition name="toast"
    ><div
      v-if="notice.text"
      class="toast-message"
      :class="notice.kind"
      role="status"
    >
      {{ notice.kind === "error" ? "!" : "✓" }} <span>{{ notice.text }}</span
      ><button aria-label="关闭提示" @click="notice.text = ''">×</button>
    </div></Transition
  >
</template>
