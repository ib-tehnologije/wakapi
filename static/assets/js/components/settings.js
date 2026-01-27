PetiteVue.createApp({
    //$delimiters: ['${', '}'],  // https://github.com/vuejs/petite-vue/pull/100
    activeTab: defaultTab,
    selectedTimezone: userTimeZone,
    vibrantColorsEnabled: JSON.parse(
        localStorage.getItem("wakapi_vibrant_colors"),
    ) || false,
    githubPatExists: typeof githubPatStored !== "undefined" ? githubPatStored : false,
    githubRepos: [],
    githubRepoLoading: false,
    githubPatMessage: "",
    githubRepoMessage: "",
    githubSuggestedId: "",
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
    apiBase() {
        const raw = window.__BASE_PATH__ || "";
        const trimmed = raw === "/" ? "" : raw.replace(/\/$/, "");
        return `${trimmed}/api`;
    },
    async saveGithubPat() {
        const tokenInput = document.getElementById("github_pat_once");
        const token = tokenInput?.value.trim();
        if (!token) {
            alert("Please paste a PAT first.");
            return;
        }
        this.githubPatMessage = "";
        try {
            const res = await fetch(`${this.apiBase()}/integrations/github/pat`, {
                method: "POST",
                headers: {
                    "Content-Type": "application/json",
                },
                body: JSON.stringify({token}),
            });
            if (!res.ok) {
                throw new Error(await res.text());
            }
            this.githubPatExists = true;
            this.githubPatMessage = "Token saved. Reusing it automatically.";
            tokenInput.value = "";
            // auto-refresh repos after saving
            this.loadGithubRepos();
        } catch (e) {
            console.error(e);
            alert("Failed to save token. Check console for details.");
        }
    },
    async removeGithubPat() {
        if (!confirm("Remove the stored GitHub token?")) {
            return;
        }
        this.githubPatMessage = "";
        try {
            const res = await fetch(`${this.apiBase()}/integrations/github/pat`, {
                method: "DELETE",
            });
            if (!res.ok) {
                throw new Error(await res.text());
            }
            this.githubPatExists = false;
            this.githubRepos = [];
            this.githubSuggestedId = "";
            this.githubRepoMessage = "Token removed. Save a new PAT to list repos.";
        } catch (e) {
            console.error(e);
            alert("Failed to remove token. Check console for details.");
        }
    },
    async loadGithubRepos() {
        if (this.githubRepoLoading) return;
        if (!this.githubPatExists) {
            this.githubRepoMessage = "Save a GitHub PAT first.";
            return;
        }
        this.githubRepoLoading = true;
        this.githubRepos = [];
        this.githubRepoMessage = "";
        try {
            const res = await fetch(`${this.apiBase()}/integrations/github/repos?all=true`);
            if (!res.ok) {
                const body = await res.text();
                if (res.status === 401) {
                    this.githubPatExists = false;
                    this.githubRepoMessage = body || "Token missing or invalid. Please save a new PAT.";
                    alert(this.githubRepoMessage);
                    return;
                }
                throw new Error(body);
            }
            this.githubRepos = await res.json();
            this.updateSuggestedRepo();
            if (this.githubRepos.length > 0) {
                this.githubRepoMessage = `Loaded ${this.githubRepos.length} repositories.`;
            }
        } catch (e) {
            console.error(e);
            alert("Failed to load repositories. Did you save a PAT?");
        } finally {
            this.githubRepoLoading = false;
        }
    },
    updateSuggestedRepo() {
        const project = document.getElementById("github_project_picker")?.value || "";
        if (!project || this.githubRepos.length === 0) {
            this.githubSuggestedId = "";
            return;
        }
        const p = project.toLowerCase();
        let best = {score: -1, id: ""};
        this.githubRepos.forEach((r) => {
            const name = (r.full_name || "").toLowerCase();
            const repoOnly = name.split("/").pop() || "";
            let score = 0;
            if (name === p || repoOnly === p) score = 100;
            else if (name.includes(p)) score = 80;
            else if (repoOnly.includes(p)) score = 70;
            else {
                // simple similarity: longer common substring length
                const common = longestCommonSubsequenceLength(repoOnly, p);
                score = common;
            }
            if (score > best.score) {
                best = {score, id: r.id};
            }
        });
        this.githubSuggestedId = best.id;
    },
    async linkGithubRepo() {
        const project = document.getElementById("github_project_picker")?.value;
        const repoId = document.getElementById("github_repo_picker")?.value;
        const branch = document.getElementById("github_branch_picker")?.value || "";
        if (!project || !repoId || this.githubRepos.length === 0) {
            alert("Select a project and repository first.");
            return;
        }
        this.githubRepoMessage = "";
        try {
            const res = await fetch(`${this.apiBase()}/integrations/github/links`, {
                method: "POST",
                headers: {"Content-Type": "application/json"},
                body: JSON.stringify({
                    project,
                    repository_id: repoId,
                    branch_override: branch,
                }),
            });
            if (!res.ok) {
                throw new Error(await res.text());
            }
            this.githubRepoMessage = "Linked. First sync is running…";
            // best effort refresh after a short delay
            setTimeout(() => window.location.reload(), 800);
        } catch (e) {
            console.error(e);
            alert("Failed to link repository. Check token permissions and repo access.");
        }
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
        const projectSelect = document.getElementById("github_project_picker");
        if (projectSelect) {
            projectSelect.addEventListener("change", () => this.updateSuggestedRepo());
        }
        if (this.githubPatExists) {
            this.loadGithubRepos();
        }
    },
}).mount("#settings-page");

function longestCommonSubsequenceLength(a, b) {
    const dp = Array(a.length + 1)
        .fill(0)
        .map(() => Array(b.length + 1).fill(0));
    for (let i = 1; i <= a.length; i++) {
        for (let j = 1; j <= b.length; j++) {
            if (a[i - 1] === b[j - 1]) dp[i][j] = dp[i - 1][j - 1] + 1;
            else dp[i][j] = Math.max(dp[i - 1][j], dp[i][j - 1]);
        }
    }
    return dp[a.length][b.length];
}
