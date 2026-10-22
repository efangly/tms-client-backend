using System.Globalization;
using System.Text.Json;
using System.Text.Json.Serialization;
using TmsBackend.Config;
using TmsBackend.Data;
using TmsBackend.Infrastructure;
using TmsBackend.Json;
using TmsBackend.Models;

namespace TmsBackend.Services;

public sealed record ArchiveResult(
    [property: JsonPropertyName("daysArchived")] int DaysArchived,
    [property: JsonPropertyName("rowsArchived")] int RowsArchived);

public sealed record RestoreResult(
    [property: JsonPropertyName("daysRestored")] int DaysRestored,
    [property: JsonPropertyName("rowsRestored")] int RowsRestored);

/// <summary>
/// Moves temp_log rows older than ARCHIVE_RETENTION_DAYS into one JSON file
/// per day ({ARCHIVE_DIR}/temp_log/YYYY/MM/temp_log_YYYY-MM-DD.json) and
/// restores ranges back into temp_log_archive for reports (archive_service.go).
/// Checks hourly and runs once per day at or after ARCHIVE_RUN_HOUR.
/// </summary>
public sealed class ArchiveService(
    IArchiveRepository repo,
    AppOptions options,
    AppReadiness readiness,
    TimeProvider time,
    ILogger<ArchiveService> logger) : BackgroundService
{
    private readonly Lock _lock = new();
    private string _lastRunDate = "";

    public int RetentionDays => options.ArchiveRetentionDays;

    protected override async Task ExecuteAsync(CancellationToken stoppingToken)
    {
        await readiness.WaitAsync(stoppingToken);
        if (!options.PollingAutostart)
            return; // e2e: archiving is triggered over HTTP only

        logger.LogInformation("Starting archive service (retention {Days} days, runs daily from {Hour}:00, dir {Dir})",
            options.ArchiveRetentionDays, options.ArchiveRunHour, options.ArchiveDir);
        using var timer = new PeriodicTimer(TimeSpan.FromHours(1), time);
        try
        {
            while (await timer.WaitForNextTickAsync(stoppingToken))
            {
                try
                {
                    await RunIfDueAsync(stoppingToken);
                }
                catch (Exception e) when (e is not OperationCanceledException)
                {
                    logger.LogError(e, "Error in archive ticker iteration (will retry next tick)");
                }
            }
        }
        catch (OperationCanceledException) when (stoppingToken.IsCancellationRequested)
        {
        }
    }

    /// <summary>Runs <see cref="RunArchiveAsync"/> at most once per calendar day, at or after ARCHIVE_RUN_HOUR.</summary>
    internal async Task RunIfDueAsync(CancellationToken ct)
    {
        var now = time.ThaiNow();
        if (now.Hour < options.ArchiveRunHour)
            return;
        var today = now.ToString("yyyy-MM-dd", CultureInfo.InvariantCulture);
        lock (_lock)
        {
            if (_lastRunDate == today)
                return;
            _lastRunDate = today;
        }

        try
        {
            await RunArchiveAsync(ct);
        }
        catch (Exception e) when (e is not OperationCanceledException)
        {
            logger.LogError("Scheduled archive run failed: {Error}", e.Message);
        }
    }

    /// <summary>Archives every not-yet-archived day of temp_log older than the retention window.</summary>
    public async Task<ArchiveResult> RunArchiveAsync(CancellationToken ct = default)
    {
        var cutoff = time.ThaiNow().AddDays(-options.ArchiveRetentionDays);
        List<string> days;
        try
        {
            days = await repo.ListDaysBeforeAsync(cutoff.ToString("yyyy-MM-dd 00:00:00", CultureInfo.InvariantCulture), ct);
        }
        catch (Exception e) when (e is not OperationCanceledException)
        {
            throw new InvalidOperationException($"failed to list archivable days: {e.Message}", e);
        }
        return await ArchiveDaysAsync(ArchiveRepository.SourceTable, days, deleteAfter: true, ct);
    }

    /// <summary>
    /// Archives every day found in a table with temp_log's schema (e.g. a renamed
    /// backup table) into the temp_log archive series. Idempotent: days already
    /// in archive_manifest are skipped.
    /// </summary>
    public async Task<ArchiveResult> ArchiveFromTableAsync(string sourceTable, bool deleteAfter, CancellationToken ct = default)
    {
        ArchiveRepository.EnsureValidTable(sourceTable);
        List<string> days;
        try
        {
            days = await repo.ListDaysAsync(sourceTable, ct);
        }
        catch (Exception e) when (e is not OperationCanceledException)
        {
            throw new InvalidOperationException($"failed to list archivable days: {e.Message}", e);
        }
        return await ArchiveDaysAsync(sourceTable, days, deleteAfter, ct);
    }

    private async Task<ArchiveResult> ArchiveDaysAsync(string sourceTable, List<string> days, bool deleteAfter, CancellationToken ct)
    {
        int daysArchived = 0, rowsArchived = 0;
        foreach (var day in days)
        {
            if (await repo.ManifestExistsAsync(day, ct))
                continue;
            try
            {
                var rows = await ArchiveDayAsync(sourceTable, day, deleteAfter, ct);
                if (rows > 0)
                {
                    daysArchived++;
                    rowsArchived += rows;
                }
            }
            catch (Exception e) when (e is not OperationCanceledException)
            {
                logger.LogError("Failed to archive {Table} for {Day}: {Error}", sourceTable, day, e.Message);
            }
        }
        if (daysArchived > 0)
            logger.LogInformation("Archived {Days} day(s), {Rows} row(s) from {Table}", daysArchived, rowsArchived, sourceTable);
        return new ArchiveResult(daysArchived, rowsArchived);
    }

    /// <summary>
    /// One day: write the JSON file, re-read it to verify the row count, then
    /// (in one transaction) delete the rows and record the manifest. The file
    /// is always filed under "temp_log", whatever table the rows came from.
    /// </summary>
    internal async Task<int> ArchiveDayAsync(string sourceTable, string day, bool deleteAfter, CancellationToken ct)
    {
        var rows = await repo.GetDayRowsAsync(sourceTable, day, ct);
        if (rows.Count == 0)
            return 0;

        var filePath = await WriteArchiveFileAsync(options.ArchiveDir, ArchiveRepository.SourceTable, day, rows, ct);
        await VerifyArchiveFileAsync(filePath, rows.Count, ct);

        await repo.CommitDayAsync(sourceTable, day, deleteAfter, new ArchiveManifest
        {
            SourceTable = ArchiveRepository.SourceTable,
            PeriodDate = day,
            FilePath = filePath,
            RowCount = rows.Count,
            ArchivedAt = time.ThaiNow(),
        }, ct);
        return rows.Count;
    }

    /// <summary>
    /// Loads archived days in [startDate, endDate] from their files into
    /// temp_log_archive. The table is cleared first, so it only ever holds the
    /// most recently restored range. Missing or unreadable files are skipped.
    /// </summary>
    public async Task<RestoreResult> RestoreRangeAsync(string startDate, string endDate, CancellationToken ct = default)
    {
        List<ArchiveManifest> manifests;
        try
        {
            manifests = await repo.ListManifestsAsync(startDate, endDate, ct);
        }
        catch (Exception e) when (e is not OperationCanceledException)
        {
            throw new InvalidOperationException($"failed to list manifests: {e.Message}", e);
        }

        var all = new List<TempLog>();
        int days = 0;
        foreach (var m in manifests)
        {
            List<TempLog>? rows;
            try
            {
                await using var stream = File.OpenRead(m.FilePath);
                rows = await JsonSerializer.DeserializeAsync<List<TempLog>>(stream, GoJson.Options, ct);
            }
            catch (Exception e) when (e is IOException or UnauthorizedAccessException)
            {
                logger.LogWarning("archive restore: skipping missing file {Path}: {Error}", m.FilePath, e.Message);
                continue;
            }
            catch (JsonException e)
            {
                logger.LogWarning("archive restore: skipping unreadable file {Path}: {Error}", m.FilePath, e.Message);
                continue;
            }
            if (rows == null || rows.Count == 0)
                continue;
            all.AddRange(rows);
            days++;
        }

        try
        {
            await repo.ReplaceArchiveRowsAsync(all, ct);
        }
        catch (Exception e) when (e is not OperationCanceledException)
        {
            throw new InvalidOperationException($"clear+restore temp_log_archive: {e.Message}", e);
        }
        return new RestoreResult(days, all.Count);
    }

    /// <summary>
    /// Writes rows as a compact JSON array and returns the path as Go stored it
    /// in archive_manifest: filepath.Join-cleaned, e.g. archives\temp_log\2026\08\temp_log_2026-08-27.json.
    /// </summary>
    internal static async Task<string> WriteArchiveFileAsync(string archiveDir, string sourceTable, string day, IReadOnlyList<TempLog> rows, CancellationToken ct)
    {
        if (!DateTime.TryParseExact(day, "yyyy-MM-dd", CultureInfo.InvariantCulture, DateTimeStyles.None, out var parsed))
            throw new ArgumentException($"invalid day \"{day}\"", nameof(day));

        var dir = GoCleanPath(Path.Join(archiveDir, sourceTable, parsed.ToString("yyyy", CultureInfo.InvariantCulture), parsed.ToString("MM", CultureInfo.InvariantCulture)));
        Directory.CreateDirectory(dir);
        var filePath = Path.Join(dir, $"{sourceTable}_{day}.json");

        await using (var stream = File.Create(filePath))
            await JsonSerializer.SerializeAsync(stream, rows, GoJson.Options, ct);
        return filePath;
    }

    /// <summary>Re-reads the file and checks the row count before rows may be deleted from the DB.</summary>
    internal static async Task VerifyArchiveFileAsync(string filePath, int expectedRows, CancellationToken ct)
    {
        List<TempLog>? rows;
        await using (var stream = File.OpenRead(filePath))
            rows = await JsonSerializer.DeserializeAsync<List<TempLog>>(stream, GoJson.Options, ct);
        if (rows?.Count != expectedRows)
            throw new InvalidDataException($"row count mismatch in {filePath}: wrote {expectedRows}, read back {rows?.Count ?? 0}");
    }

    /// <summary>Drops "." segments like Go's filepath.Clean ("./archives/x" → "archives\x").</summary>
    private static string GoCleanPath(string path)
    {
        var sep = Path.DirectorySeparatorChar;
        var normalized = path.Replace('/', sep).Replace('\\', sep);
        var rooted = Path.IsPathRooted(normalized);
        var parts = normalized.Split(sep).Where((p, i) => p != "." && (p != "" || (i == 0 && rooted)));
        return string.Join(sep, parts);
    }
}
