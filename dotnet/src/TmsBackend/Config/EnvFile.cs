namespace TmsBackend.Config;

/// <summary>
/// Loads .env into the process environment, like godotenv.Load in main.go:
/// values already set in the real environment win (no clobber).
/// </summary>
public static class EnvFile
{
    /// <summary>
    /// Tries .env next to the exe first, then .env in the directory the process
    /// was launched from (covers `dotnet run`, where the exe lives in bin/).
    /// Returns the path that was loaded, or null when neither exists.
    /// </summary>
    public static string? Load(string exeDir, string? launchDir)
    {
        foreach (var dir in new[] { exeDir, launchDir })
        {
            if (string.IsNullOrEmpty(dir))
                continue;
            var path = Path.Combine(dir, ".env");
            if (!File.Exists(path))
                continue;
            DotNetEnv.Env.NoClobber().Load(path);
            return path;
        }
        return null;
    }
}
