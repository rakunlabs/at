<script lang="ts">
  interface Props {
    period?: string;
    resetDay?: number;
    resetTime?: string;
    timezone?: string;
  }

  let {
    period = $bindable('monthly'),
    resetDay = $bindable(1),
    resetTime = $bindable('00:00'),
    timezone = $bindable('UTC'),
  }: Props = $props();

  const weekdays = [
    { value: 1, label: 'Monday' },
    { value: 2, label: 'Tuesday' },
    { value: 3, label: 'Wednesday' },
    { value: 4, label: 'Thursday' },
    { value: 5, label: 'Friday' },
    { value: 6, label: 'Saturday' },
    { value: 7, label: 'Sunday' },
  ];

  const timezones = [
    'UTC',
    'Europe/Istanbul',
    'Europe/London',
    'Europe/Berlin',
    'America/New_York',
    'America/Chicago',
    'America/Los_Angeles',
    'Asia/Dubai',
    'Asia/Tokyo',
  ];

  function handlePeriodChange(event: Event) {
    period = (event.target as HTMLSelectElement).value;
    if (period === 'daily') resetDay = 0;
    else if (resetDay < 1 || (period === 'weekly' && resetDay > 7)) resetDay = 1;
  }
</script>

<div class="grid grid-cols-2 lg:grid-cols-4 gap-3">
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider block mb-0.5">Period</span>
    <select
      value={period}
      onchange={handlePeriodChange}
      class="w-full px-2 py-1 text-xs border border-gray-300 dark:border-dark-border-subtle rounded focus:outline-none focus:ring-1 focus:ring-gray-400 dark:bg-dark-elevated dark:text-dark-text"
    >
      <option value="daily">Daily</option>
      <option value="weekly">Weekly</option>
      <option value="monthly">Monthly</option>
    </select>
  </label>

  {#if period === 'weekly'}
    <label class="block">
      <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider block mb-0.5">Reset day</span>
      <select
        bind:value={resetDay}
        class="w-full px-2 py-1 text-xs border border-gray-300 dark:border-dark-border-subtle rounded focus:outline-none focus:ring-1 focus:ring-gray-400 dark:bg-dark-elevated dark:text-dark-text"
      >
        {#each weekdays as day}
          <option value={day.value}>{day.label}</option>
        {/each}
      </select>
    </label>
  {:else if period === 'monthly'}
    <label class="block">
      <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider block mb-0.5">Reset day</span>
      <input
        type="number"
        min="1"
        max="31"
        bind:value={resetDay}
        class="w-full px-2 py-1 text-xs border border-gray-300 dark:border-dark-border-subtle rounded focus:outline-none focus:ring-1 focus:ring-gray-400 dark:bg-dark-elevated dark:text-dark-text"
      />
      <span class="text-[9px] text-gray-400 dark:text-dark-text-muted">Short months use their last day.</span>
    </label>
  {/if}

  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider block mb-0.5">Reset time</span>
    <input
      type="time"
      bind:value={resetTime}
      class="w-full px-2 py-1 text-xs border border-gray-300 dark:border-dark-border-subtle rounded focus:outline-none focus:ring-1 focus:ring-gray-400 dark:bg-dark-elevated dark:text-dark-text"
    />
  </label>

  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider block mb-0.5">Timezone</span>
    <input
      type="text"
      list="budget-timezones"
      bind:value={timezone}
      placeholder="UTC"
      class="w-full px-2 py-1 text-xs font-mono border border-gray-300 dark:border-dark-border-subtle rounded focus:outline-none focus:ring-1 focus:ring-gray-400 dark:bg-dark-elevated dark:text-dark-text"
    />
    <datalist id="budget-timezones">
      {#each timezones as zone}<option value={zone}></option>{/each}
    </datalist>
  </label>
</div>
