using Microsoft.Extensions.Logging;
using TmsBackend.Logging;

namespace TmsBackend.Tests.Logging;

public class ErrorFileLoggerProviderTests : IDisposable
{
    private readonly string _dir = Directory.CreateTempSubdirectory().FullName;

    public void Dispose() => Directory.Delete(_dir, true);

    [Fact]
    public void Writes_only_errors_to_daily_file()
    {
        var now = new DateTime(2026, 2, 2, 10, 30, 45);
        using (var provider = new ErrorFileLoggerProvider(_dir, () => now))
        {
            var logger = provider.CreateLogger("TmsBackend.Services.PollingService");
            logger.LogInformation("not written");
            logger.LogWarning("not written either");
            logger.LogError("pollAndSave - Failed to load devices: {Reason}", "connection refused");
        }

        var lines = File.ReadAllLines(Path.Combine(_dir, "error_2026-02-02.txt"));
        Assert.Equal(["[ERROR] 2026/02/02 10:30:45 PollingService: pollAndSave - Failed to load devices: connection refused"], lines);
    }

    [Fact]
    public void Rotates_daily_and_deletes_files_older_than_30_days()
    {
        var old = Path.Combine(_dir, "error_2025-12-01.txt");
        File.WriteAllText(old, "old");
        File.SetLastWriteTime(old, new DateTime(2025, 12, 1));
        var unrelated = Path.Combine(_dir, "keep.txt");
        File.WriteAllText(unrelated, "x");
        File.SetLastWriteTime(unrelated, new DateTime(2025, 1, 1));

        var now = new DateTime(2026, 2, 2, 23, 59, 59);
        using (var provider = new ErrorFileLoggerProvider(_dir, () => now))
        {
            var logger = provider.CreateLogger("Test");
            logger.LogError("day one");
            now = now.AddSeconds(1);
            logger.LogError("day two");
        }

        Assert.False(File.Exists(old));
        Assert.True(File.Exists(unrelated));
        Assert.Contains("day one", File.ReadAllText(Path.Combine(_dir, "error_2026-02-02.txt")));
        Assert.Contains("day two", File.ReadAllText(Path.Combine(_dir, "error_2026-02-03.txt")));
    }
}
