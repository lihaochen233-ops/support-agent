<script setup lang="ts">
defineProps<{ compact?: boolean; light?: boolean }>();
</script>
<template>
  <RouterLink to="/" class="brand" :class="{ light }"
    ><span class="brand-mark"><i></i><i></i><i></i><i></i></span
    ><span v-if="!compact" class="brand-word"
      >luma<span>智能客服</span></span
    ></RouterLink
  >
</template>
