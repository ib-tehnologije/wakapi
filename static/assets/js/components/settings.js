PetiteVue.createApp({
    //$delimiters: ['${', '}'],  // https://github.com/vuejs/petite-vue/pull/100
    activeTab: defaultTab,
    selectedTimezone: userTimeZone,
    vibrantColorsEnabled: JSON.parse(
        localStorage.getItem("wakapi_vibrant_colors"),
    ) || false,
    labels: {},
    get tzOptions() {
        return [
            defaultTzOption,
            ...tzs.sort().map((tz) => ({value: tz, text: tz})),
        ];
    },
    updateTab() {
        this.activeTab = window.location.hash.slice(1) || defaultTab;
    },
    isActive(tab) {
        return this.activeTab === tab;
    },
    confirmChangeUsername() {
        if (confirm("Are you sure? This cannot be undone.")) {
            document.querySelector("#form-change-username").submit();
        }
    },
    confirmRegenerate() {
        if (confirm("Are you sure?")) {
            document.querySelector("#form-regenerate-summaries").submit();
        }
    },
    confirmWakatimeImport() {
        if (confirm("Are you sure? The import can not be undone.")) {
            // weird hack to sync the "legacy importer" form field from the wakatime connection form to the (invisible) import form
            document.getElementById('use_legacy_importer').value = document.getElementById('use_legacy_importer_tmp').checked.toString()
            document.querySelector("#form-import-wakatime").submit();
        }
    },
    confirmClearData() {
        if (confirm("Are you sure? This can not be undone!")) {
            document.querySelector("#form-clear-data").submit();
        }
    },
    confirmDeleteAccount() {
        if (confirm("Are you sure? This can not be undone!")) {
            document.querySelector("#form-delete-user").submit();
        }
    },
    confirmUnlinkWithPurge(event) {
        const form = event.target.closest("form");
        const purgeChecked = form?.querySelector('input[name="github_purge"]')?.checked;
        if (purgeChecked) {
            if (!confirm("This will delete stored commits & stats. Continue?")) {
                event.preventDefault();
            }
        }
    },
    attachGitHubSyncButtons() {
        document
            .querySelectorAll('form input[name="action"][value="sync_github_project"]')
            .forEach((input) => {
                const form = input.closest("form");
                const button = form?.querySelector('button[type="submit"]');
                if (!button) return;
                form.addEventListener("submit", () => {
                    const project = form.querySelector('input[name="github_project_sync"]')?.value;
                    const row = document.querySelector(`[data-project="${project}"]`);
                    const cell = row?.querySelector("[data-sync-cell]");

                    button.disabled = true;
                    const original = button.textContent;
                    button.textContent = "Syncing...";
                    if (cell) {
                        cell.textContent = "syncing…";
                    }
                    setTimeout(() => {
                        button.disabled = false;
                        button.textContent = original;
                    }, 8000); // fallback reset if page stays
                });
            });
    },
    onToggleVibrantColors() {
        localStorage.setItem("wakapi_vibrant_colors", this.vibrantColorsEnabled);
    },
    showProjectAddButton(index) {
        this.labels[index] = true;
    },
    mounted() {
        this.updateTab();
        window.addEventListener("hashchange", () => this.updateTab());
        this.attachGitHubSyncButtons();
    },
}).mount("#settings-page");
