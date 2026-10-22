using System.Globalization;
using Dapper;
using TmsBackend.Models;

namespace TmsBackend.Data;

/// <summary>
/// Raw temp_log / temp_log_archive row. temp_value is FLOAT in temp_log but
/// DOUBLE in temp_log_archive, so it is read untyped and normalized.
/// </summary>
internal sealed class TempLogRow
{
    public string MachineIP { get; set; } = "";
    public int ProbeNo { get; set; }
    public string? McuID { get; set; }
    public object? TempValue { get; set; }
    public int? RealValue { get; set; }
    public string? Status { get; set; }
    public DateTime? SendTime { get; set; }
    public DateTime InsertTime { get; set; }
    public string? SDate { get; set; }
    public string? STime { get; set; }

    /// <summary>
    /// FLOAT 25.4 widened directly to double is 25.399999618530273; going
    /// through float's shortest round-trip string gives back 25.4.
    /// </summary>
    public static double? ToDouble(object? v) => v switch
    {
        null or DBNull => null,
        float f => double.Parse(f.ToString(CultureInfo.InvariantCulture), CultureInfo.InvariantCulture),
        double d => d,
        decimal m => (double)m,
        _ => Convert.ToDouble(v, CultureInfo.InvariantCulture),
    };

    public TempLog ToModel() => new()
    {
        MachineIP = MachineIP,
        ProbeNo = ProbeNo,
        McuID = McuID,
        TempValue = ToDouble(TempValue),
        RealValue = RealValue,
        Status = Status,
        SendTime = SendTime,
        InsertTime = InsertTime,
        SDate = SDate,
        STime = STime,
    };
}

/// <summary>Filter for listing temp_log / temp_log_archive rows.</summary>
public sealed record TempLogQuery
{
    /// <summary>YYYY-MM-DD; applied only when both dates are set (as in Go).</summary>
    public string? StartDate { get; init; }
    public string? EndDate { get; init; }
    public DeviceFilter? Devices { get; init; }
    public bool Descending { get; init; }
    /// <summary>GORM semantics: null or negative = no limit, 0 = LIMIT 0.</summary>
    public int? Limit { get; init; }
}

public interface ITempLogRepository
{
    /// <summary>Throws a duplicate-key MySqlException (see <see cref="Db.IsDuplicateKey"/>) when the row exists.</summary>
    Task InsertAsync(TempLog log, CancellationToken ct = default);
    Task<List<TempLog>> QueryAsync(TempLogQuery query, bool fromArchive = false, CancellationToken ct = default);
    Task<TempLog?> GetLatestAsync(string machineIp, int probeNo, CancellationToken ct = default);
}

public sealed class TempLogRepository(Db db) : ITempLogRepository
{
    public async Task InsertAsync(TempLog l, CancellationToken ct = default)
    {
        var (sql, p) = SqlInsert.Build("temp_log",
        [
            new("machine_ip", l.MachineIP),
            new("probe_no", l.ProbeNo, HasDefault: true),
            new("mcu_id", l.McuID),
            new("temp_value", l.TempValue),
            new("real_value", l.RealValue),
            new("status", l.Status),
            new("send_time", l.SendTime),
            new("insert_time", l.InsertTime),
            new("sDate", l.SDate),
            new("sTime", l.STime),
        ]);
        await using var conn = await db.OpenAsync(ct);
        await conn.ExecuteAsync(new CommandDefinition(sql, p, cancellationToken: ct));
    }

    public async Task<List<TempLog>> QueryAsync(TempLogQuery q, bool fromArchive = false, CancellationToken ct = default)
    {
        var (sql, p) = BuildQuery(q, fromArchive ? "temp_log_archive" : "temp_log");
        await using var conn = await db.OpenAsync(ct);
        var rows = await conn.QueryAsync<TempLogRow>(new CommandDefinition(sql, p, cancellationToken: ct));
        return rows.Select(r => r.ToModel()).ToList();
    }

    internal static (string Sql, DynamicParameters Params) BuildQuery(TempLogQuery q, string table)
    {
        var p = new DynamicParameters();
        var where = new List<string>();
        if (!string.IsNullOrEmpty(q.StartDate) && !string.IsNullOrEmpty(q.EndDate))
        {
            // Plain strings, not DateTime, so the window matches insert_time's wall-clock values.
            where.Add("insert_time BETWEEN @start AND @end");
            p.Add("start", q.StartDate + " 00:00:00");
            p.Add("end", q.EndDate + " 23:59:59");
        }
        if (q.Devices != null)
            where.Add(q.Devices.ToSql(p));

        var sql = $"SELECT * FROM {table}";
        if (where.Count > 0)
            sql += " WHERE " + string.Join(" AND ", where);
        sql += q.Descending ? " ORDER BY insert_time DESC" : " ORDER BY insert_time ASC";
        if (q.Limit is >= 0)
        {
            sql += " LIMIT @limit";
            p.Add("limit", q.Limit.Value);
        }
        return (sql, p);
    }

    public async Task<TempLog?> GetLatestAsync(string machineIp, int probeNo, CancellationToken ct = default)
    {
        await using var conn = await db.OpenAsync(ct);
        var row = await conn.QueryFirstOrDefaultAsync<TempLogRow>(new CommandDefinition(
            "SELECT * FROM temp_log WHERE machine_ip = @machineIp AND probe_no = @probeNo ORDER BY insert_time DESC LIMIT 1",
            new { machineIp, probeNo }, cancellationToken: ct));
        return row?.ToModel();
    }
}
