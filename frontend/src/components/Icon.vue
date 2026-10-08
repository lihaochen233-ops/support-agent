<script setup lang="ts">
withDefaults(defineProps<{ name: string; size?: number }>(), { size: 20 });
const paths: Record<string, string> = {
  chat: "M21 11.5a8.5 8.5 0 0 1-8.5 8.5H4l-3 3V11.5A8.5 8.5 0 0 1 9.5 3H12a8.5 8.5 0 0 1 9 8.5ZM6 9h10M6 13h7",
  spark: "m12 3 2.5 6.5L21 12l-6.5 2.5L12 21l-2.5-6.5L3 12l6.5-2.5L12 3Z",
  arrow: "M5 12h14m-6-6 6 6-6 6",
  send: "m22 2-7 20-4-9-9-4L22 2ZM22 2 11 13",
  image:
    "M4 3h16a1 1 0 0 1 1 1v16a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1Zm-1 13 5-5 4 4 3-3 6 6M8 7h.01",
  user: "M16 7a4 4 0 1 1-8 0 4 4 0 0 1 8 0ZM4 21v-2a8 8 0 0 1 16 0v2",
  users:
    "M16 21v-2a6 6 0 0 0-12 0v2M14 3a4 4 0 0 1 0 8M20 21v-2a6 6 0 0 0-3-5M14 7a4 4 0 1 1-8 0 4 4 0 0 1 8 0Z",
  book: "M12 5c-4-3-8-2-10-1v16c3-2 7-2 10 0 3-2 7-2 10 0V4c-3-1-7-2-10 1Zm0 0v15",
  grid: "M3 3h7v7H3V3Zm11 0h7v7h-7V3ZM3 14h7v7H3v-7Zm11 0h7v7h-7v-7Z",
  log: "M4 3h16v18H4V3Zm4 5h8m-8 4h8m-8 4h5",
  logout: "M9 3H3v18h6m5-15 6 6-6 6m-5-6h11",
  search: "M21 21l-6-6M17 10a7 7 0 1 1-14 0 7 7 0 0 1 14 0Z",
  plus: "M12 5v14M5 12h14",
  close: "m6 6 12 12M6 18 18 6",
  check: "m5 12 4 4L19 6",
  clock: "M12 7v5l3 2M22 12A10 10 0 1 1 2 12a10 10 0 0 1 20 0Z",
  chevron: "m9 6 6 6-6 6",
  upload: "M12 16V3m-5 5 5-5 5 5M3 15v6h18v-6",
  refresh: "M20 7a9 9 0 1 0 1 8M20 3v5h-5",
  shield: "M12 2 3 6v6c0 5 9 10 9 10s9-5 9-10V6l-9-4Zm-4 10 3 3 5-6",
  mail: "M3 5h18v14H3V5Zm0 0 9 7 9-7",
  lock: "M5 10h14v11H5V10Zm3 0V6a4 4 0 0 1 8 0v4",
  menu: "M4 6h16M4 12h16M4 18h16",
  back: "M19 12H5m6-6-6 6 6 6",
  file: "M5 2h9l5 5v15H5V2Zm9 0v5h5M8 12h8m-8 4h6",
  headset:
    "M3 13v-1a9 9 0 0 1 18 0v1M3 12h4v8H3v-8Zm14 0h4v8h-4v-8Zm4 8c0 2-4 2-7 2",
  activity: "M2 12h4l3-9 6 18 3-9h4",
  thumb: "M8 10 12 2c3 0 2 5 2 7h6l1 2-3 10H8V10ZM3 10h5v11H3V10Z",
  trash: "M3 6h18M9 6V3h6v3M5 6l1 15h12l1-15M10 10v7m4-7v7",
  moon: "M20 14A8 8 0 0 1 10 4 9 9 0 1 0 20 14Z",
};
</script>
<template>
  <svg
    :width="size"
    :height="size"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="1.65"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
  >
    <path :d="paths[name] || paths.chat" />
  </svg>
</template>
