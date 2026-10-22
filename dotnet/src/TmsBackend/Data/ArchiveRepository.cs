using System.Text.RegularExpressions;
using Dapper;
using TmsBackend.Models;

namespace TmsBackend.Data;

public interface IArchiveRepository
{
    /// <summary>Distinct YYYY-MM-DD days in temp_log strictly before <paramref name="cutoff"/> ("YYYY-MM-DD 00:00:00").</summary>
    Task<List<string>> ListDaysBeforeAsync(string cutoff, CancellationToken ct = default);
    /// <summary>Distinct YYYY-MM-DD days in an arbitrary table with temp_log's schema.</summary>
    Task<List<string>> ListDaysAsync(string table, CancellationToken ct = default);
    Task<bool> ManifestExistsAsync(string day, CancellationToken ct = default);
    Task<List<TempLog>> GetDayRowsAsync(string table, string day, CancellationToken ct = default);
    /// <summary>In one transaction: optionally delete the day's rows from <paramref name="table"/>, then upsert the manifest.</summary>
    Task CommitDayAsync(string table, string day, bool deleteRows, ArchiveManifest manifest, CancellationToken ct = default);
    /// <summary>temp_log manifests, newest first, optionally limited to [start, end].</summary>
    Task<List<ArchiveManifest>> ListManifestsAsync(string? startDate, string? endDate, CancellationToken ct = default);
    /// <summary>In one transaction: clear temp_log_archive, then insert <paramref name="rows"/> (duplicates ignored).</summary>
    Task ReplaceArchiveRowsAsync(IReadOnlyList<TempLog> rows, CancellationToken ct = default);
}

public sealed partial class ArchiveRepository(Db db) : IArchiveRepository
{
    // Archive files and manifests are always filed under this source, even
    // when the rows come from a differently named backup table.
    public const string SourceTable = "temp_log";

    private const int InsertBatchSize = 500;

    [GeneratedRegex("^[A-Za-z0-9_]+$")]
    private static partial Regex ValidTableName();

    /// <summary>Guards table names interpolated into SQL (validTableName in archive_service.go).</summary>
    public static void EnsureValidTable(string table)
    {
        if (!ValidTableName().IsMatch(table))
            throw new ArgumentException($"invalid source table name \"{table}\"", nameof(table));
    }

    public async Task<List<string>> ListDaysBeforeAsync(string cutoff, CancellationToken ct = default)
    {
        await using var conn = await db.OpenAsync(ct);
        // DATE_FORMAT (not DATE()) so the column comes back as a plain string.
        return (await conn.QueryAsync<string>(new CommandDefinition(
            "SELECT DISTINCT DATE_FORMAT(insert_time, '%Y-%m-%d') AS day FROM temp_log WHERE insert_time < @cutoff ORDER BY day",
            new { cutoff }, cancellationToken: ct))).AsList();
    }

    public async Task<List<string>> ListDaysAsync(string table, CancellationToken ct = default)
    {
        EnsureValidTable(table);
        await using var conn = await db.OpenAsync(ct);
        return (await conn.QueryAsync<string>(new CommandDefinition(
            $"SELECT DISTINCT DATE_FORMAT(insert_time, '%Y-%m-%d') AS day FROM `{table}` ORDER BY day",
            cancellationToken: ct))).AsList();
    }

    public async Task<bool> ManifestExistsAsync(string day, CancellationToken ct = default)
    {
        await using var conn = await db.OpenAsync(ct);
        return await conn.ExecuteScalarAsync<int?>(new CommandDefinition(
            "SELECT 1 FROM archive_manifest WHERE source_table = @src AND period_date = @day LIMIT 1",
            new { src = SourceTable, day }, cancellationToken: ct)) != null;
    }

    public async Task<List<TempLog>> GetDayRowsAsync(string table, string day, CancellationToken ct = default)
    {
        EnsureValidTable(table);
        await using var conn = await db.OpenAsync(ct);
        var rows = await conn.QueryAsync<TempLogRow>(new CommandDefinition(
            $"SELECT * FROM `{table}` WHERE insert_time BETWEEN @start AND @end ORDER BY insert_time ASC",
            new { start = day + " 00:00:00", end = day + " 23:59:59" }, cancellationToken: ct));
        return rows.Select(r => r.ToModel()).ToList();
    }

    public async Task CommitDayAsync(string table, string day, bool deleteRows, ArchiveManifest m, CancellationToken ct = default)
    {
        EnsureValidTable(table);
        await using var conn = await db.OpenAsync(ct);
        await using var tx = await conn.BeginTransactionAsync(ct);
        if (deleteRows)
        {
            await conn.ExecuteAsync(new CommandDefinition(
                $"DELETE FROM `{table}` WHERE insert_time BETWEEN @start AND @end",
                new { start = day + " 00:00:00", end = day + " 23:59:59" }, tx, cancellationToken: ct));
        }
        await conn.ExecuteAsync(new CommandDefinition("""
            INSERT INTO archive_manifest (source_table, period_date, file_path, row_count, archived_at)
            VALUES (@SourceTable, @PeriodDate, @FilePath, @RowCount, @ArchivedAt)
            ON DUPLICATE KEY UPDATE file_path = VALUES(file_path), row_count = VALUES(row_count), archived_at = VALUES(archived_at)
            """, m, tx, cancellationToken: ct));
        await tx.CommitAsync(ct);
    }

    public async Task<List<ArchiveManifest>> ListManifestsAsync(string? startDate, string? endDate, CancellationToken ct = default)
    {
        var sql = "SELECT * FROM archive_manifest WHERE source_table = @src";
        if (!string.IsNullOrEmpty(startDate) && !string.IsNullOrEmpty(endDate))
            sql += " AND period_date BETWEEN @startDate AND @endDate";
        sql += " ORDER BY period_date DESC";
        await using var conn = await db.OpenAsync(ct);
        return (await conn.QueryAsync<ArchiveManifest>(new CommandDefinition(
            sql, new { src = SourceTable, startDate, endDate }, cancellationToken: ct))).AsList();
    }

    public async Task ReplaceArchiveRowsAsync(IReadOnlyList<TempLog> rows, CancellationToken ct = default)
    {
        await using var conn = await db.OpenAsync(ct);
        await using var tx = await conn.BeginTransactionAsync(ct);
        await conn.ExecuteAsync(new CommandDefinition("DELETE FROM temp_log_archive", transaction: tx, cancellationToken: ct));
        foreach (var batch in rows.Chunk(InsertBatchSize))
        {
            var (sql, p) = BuildArchiveInsert(batch);
            await conn.ExecuteAsync(new CommandDefinition(sql, p, tx, cancellationToken: ct));
        }
        await tx.CommitAsync(ct);
    }

    internal static (string Sql, DynamicParameters Params) BuildArchiveInsert(IReadOnlyList<TempLog> batch)
    {
        var p = new DynamicParameters();
        var values = new List<string>(batch.Count);
        for (var i = 0; i < batch.Count; i++)
        {
            var r = batch[i];
            values.Add($"(@ip{i}, @probe{i}, @mcu{i}, @temp{i}, @real{i}, @status{i}, @send{i}, @insert{i}, @sdate{i}, @stime{i})");
            p.Add($"ip{i}", r.MachineIP);
            p.Add($"probe{i}", r.ProbeNo);
            p.Add($"mcu{i}", r.McuID);
            p.Add($"temp{i}", r.TempValue);
            p.Add($"real{i}", r.RealValue);
            p.Add($"status{i}", r.Status);
            p.Add($"send{i}", r.SendTime);
            p.Add($"insert{i}", r.InsertTime);
            p.Add($"sdate{i}", r.SDate);
            p.Add($"stime{i}", r.STime);
        }
        // GORM OnConflict{DoNothing} on MySQL: a no-op update on the first key column.
        var sql = "INSERT INTO temp_log_archive (machine_ip, probe_no, mcu_id, temp_value, real_value, status, send_time, insert_time, sDate, sTime) VALUES "
            + string.Join(", ", values)
            + " ON DUPLICATE KEY UPDATE machine_ip = machine_ip";
        return (sql, p);
    }
}
