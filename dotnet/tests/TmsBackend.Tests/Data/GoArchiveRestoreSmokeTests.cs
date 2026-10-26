using System.Text.Json;
using Dapper;
using Microsoft.Extensions.Logging.Abstractions;
using TmsBackend.Config;
using TmsBackend.Data;
using TmsBackend.Infrastructure;
using TmsBackend.Json;
using TmsBackend.Models;
using TmsBackend.Services;

namespace TmsBackend.Tests.Data;

/// <summary>
/// Restores archive files written by the Go build into temp_log_archive and
/// compares every row with the file. Opt-in and destructive (replaces
/// temp_log_archive and the manifests in the restored range), so it only runs
/// against a test database:
///   TMS_ARCHIVE_SMOKE  = path of a .env whose DB_NAME contains "test" or "e2e"
///   TMS_GO_ARCHIVE_DIR = a Go archives\temp_log folder (the newest 3 files are used)
/// </summary>
[Collection(nameof(TmsBackend.Tests.Config.EnvironmentCollection))]
public class GoArchiveRestoreSmokeTests
{
    [Fact]
    public async Task Restores_go_archive_files_row_for_row()
    {
        var envPath = Environment.GetEnvironmentVariable("TMS_ARCHIVE_SMOKE");
        var goDir = Environment.GetEnvironmentVariable("TMS_GO_ARCHIVE_DIR");
        if (string.IsNullOrEmpty(envPath) || string.IsNullOrEmpty(goDir))
            return;

        DotNetEnv.Env.NoClobber().Load(envPath);
        var options = AppOptions.FromEnvironment();
        Assert.True(options.DbName.Contains("test", StringComparison.OrdinalIgnoreCase)
                    || options.DbName.Contains("e2e", StringComparison.OrdinalIgnoreCase),
            $"refusing to restore into \"{options.DbName}\": not a test database");

        var files = Directory.GetFiles(goDir, "temp_log_*.json", SearchOption.AllDirectories)
            .OrderBy(Path.GetFileName, StringComparer.Ordinal).TakeLast(3).ToList();
        Assert.NotEmpty(files);
        var days = files.Select(f => Path.GetFileNameWithoutExtension(f)["temp_log_".Length..]).ToList();

        var expected = new List<TempLog>();
        foreach (var f in files)
        {
            await using var s = File.OpenRead(f);
            expected.AddRange((await JsonSerializer.DeserializeAsync<List<TempLog>>(s, GoJson.Options))!);
        }

        await using var db = new Db(options);
        var repo = new ArchiveRepository(db);
        await using (var conn = await db.OpenAsync())
        {
            await conn.ExecuteAsync("DELETE FROM archive_manifest WHERE source_table = 'temp_log' AND period_date BETWEEN @a AND @b",
                new { a = days[0], b = days[^1] });
        }
        for (var i = 0; i < files.Count; i++)
        {
            await repo.CommitDayAsync(ArchiveRepository.SourceTable, days[i], deleteRows: false, new ArchiveManifest
            {
                SourceTable = ArchiveRepository.SourceTable,
                PeriodDate = days[i],
                FilePath = Path.GetFullPath(files[i]),
                RowCount = 0,
                ArchivedAt = DateTime.Now,
            });
        }

        try
        {
            var service = new ArchiveService(repo, options, new AppReadiness(), TimeProvider.System, NullLogger<ArchiveService>.Instance);
            var result = await service.RestoreRangeAsync(days[0], days[^1]);
            Assert.Equal(files.Count, result.DaysRestored);
            Assert.Equal(expected.Count, result.RowsRestored);

            var restored = await new TempLogRepository(db).QueryAsync(
                new TempLogQuery { StartDate = days[0], EndDate = days[^1] }, fromArchive: true);

            // Duplicate rows in a file collapse to one (ON DUPLICATE KEY), so compare distinct rows.
            var want = expected.Select(Key).Distinct().Order(StringComparer.Ordinal).ToList();
            var got = restored.Select(Key).Order(StringComparer.Ordinal).ToList();
            Assert.Equal(want.Count, got.Count);
            Assert.Equal(want, got);
        }
        finally
        {
            await using var conn = await db.OpenAsync();
            await conn.ExecuteAsync("DELETE FROM archive_manifest WHERE source_table = 'temp_log' AND period_date BETWEEN @a AND @b",
                new { a = days[0], b = days[^1] });
            await conn.ExecuteAsync("DELETE FROM temp_log_archive");
        }
    }

    private static string Key(TempLog l) => JsonSerializer.Serialize(l, GoJson.Options);
}
