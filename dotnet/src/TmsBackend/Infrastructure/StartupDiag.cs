using System.Globalization;

namespace TmsBackend.Infrastructure;

/// <summary>
/// Appends early-startup breadcrumbs to %TEMP%\tms-backend-startup.log and
/// startup_debug.log next to the exe, before logging is configured. Mirrors
/// startupDiag in main.go; useful when a service fails to start.
/// </summary>
public static class StartupDiag
{
    // Cap each file so it doesn't grow forever across many service restarts.
    private const long MaxSize = 200 * 1024;

    public static void Write(string message)
    {
        var line = string.Create(CultureInfo.InvariantCulture, $"{DateTime.Now:yyyy-MM-dd HH:mm:ss} {message}{Environment.NewLine}");

        var tmp = Environment.GetEnvironmentVariable("TEMP")
            ?? Environment.GetEnvironmentVariable("TMP")
            ?? @"C:\Windows\Temp";
        Append(Path.Combine(tmp, "tms-backend-startup.log"), line);
        Append(Path.Combine(AppContext.BaseDirectory, "startup_debug.log"), line);
    }

    private static void Append(string path, string line)
    {
        try
        {
            var info = new FileInfo(path);
            if (info.Exists && info.Length > MaxSize)
                info.Delete();
            File.AppendAllText(path, line);
        }
        catch
        {
            // Best effort only: diagnostics must never break startup.
        }
    }
}
