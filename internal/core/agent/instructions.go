package agent

// InstallOffer is the project instruction that installation writes into each
// agent's instruction file and doctor looks for.
const InstallOffer = "<!-- duolog --> If `.duolog/config.yaml` exists and `duolog` is not on PATH, offer the user this install, and run it only if they agree; macOS/Linux: `curl -fsSL https://github.com/lorem-dev/duolog/releases/latest/download/install.sh | sh && duolog install --scope project`; Windows PowerShell: `irm https://github.com/lorem-dev/duolog/releases/latest/download/install.ps1 | iex` then add the printed directory to PATH and run `duolog install --scope project`."
