using Dapper;
using TmsBackend.Models;

namespace TmsBackend.Data;

/// <summary>Latest temp_log reading of one probe (GetMachines in handlers.go).</summary>
public sealed record LatestReading(string MachineIP, int ProbeNo, double? TempValue, DateTime InsertTime);

public interface IMachineRepository
{
    Task<List<MasterMachine>> GetAllAsync(CancellationToken ct = default);
    Task<MasterMachine?> GetAsync(string machineIp, int probeNo, CancellationToken ct = default);
    /// <summary>First probe (by primary key order) of a machine IP.</summary>
    Task<MasterMachine?> GetFirstByIpAsync(string machineIp, CancellationToken ct = default);
    Task CreateAsync(MasterMachine machine, CancellationToken ct = default);
    Task UpdateAsync(string machineIp, int probeNo, IReadOnlyDictionary<string, object?> columns, CancellationToken ct = default);
    Task DeleteAsync(string machineIp, int? probeNo, CancellationToken ct = default);
    Task UpdateColorAsync(string machineIp, int probeNo, string color, CancellationToken ct = default);
    Task UpdateChkMonAsync(string machineIp, int probeNo, string value, CancellationToken ct = default);
    Task<List<LatestReading>> GetLatestReadingsAsync(CancellationToken ct = default);
}

public sealed class MachineRepository(Db db) : IMachineRepository
{
    public async Task<List<MasterMachine>> GetAllAsync(CancellationToken ct = default)
    {
        await using var conn = await db.OpenAsync(ct);
        return (await conn.QueryAsync<MasterMachine>(new CommandDefinition("SELECT * FROM master_machine", cancellationToken: ct))).AsList();
    }

    public async Task<MasterMachine?> GetAsync(string machineIp, int probeNo, CancellationToken ct = default)
    {
        await using var conn = await db.OpenAsync(ct);
        return await conn.QueryFirstOrDefaultAsync<MasterMachine>(new CommandDefinition(
            "SELECT * FROM master_machine WHERE machine_ip = @machineIp AND probe_no = @probeNo ORDER BY machine_ip, probe_no LIMIT 1",
            new { machineIp, probeNo }, cancellationToken: ct));
    }

    public async Task<MasterMachine?> GetFirstByIpAsync(string machineIp, CancellationToken ct = default)
    {
        await using var conn = await db.OpenAsync(ct);
        return await conn.QueryFirstOrDefaultAsync<MasterMachine>(new CommandDefinition(
            "SELECT * FROM master_machine WHERE machine_ip = @machineIp ORDER BY machine_ip, probe_no LIMIT 1",
            new { machineIp }, cancellationToken: ct));
    }

    public async Task CreateAsync(MasterMachine m, CancellationToken ct = default)
    {
        // HasDefault follows the gorm:"default:..." tags in models.go.
        var (sql, p) = SqlInsert.Build("master_machine",
        [
            new("machine_ip", m.MachineIP),
            new("probe_no", m.ProbeNo, HasDefault: true),
            new("probe_all", m.ProbeAll, HasDefault: true),
            new("machine_name", m.MachineName),
            new("color", m.Color, HasDefault: true),
            new("chkOnline", m.ChkOnline, HasDefault: true),
            new("chkSms", m.ChkSms, HasDefault: true),
            new("chkMail", m.ChkMail, HasDefault: true),
            new("chkMon", m.ChkMon, HasDefault: true),
            new("chkLine", m.ChkLine, HasDefault: true),
            new("chkReport", m.ChkReport, HasDefault: true),
            new("min_temp", m.MinTemp),
            new("max_temp", m.MaxTemp),
            new("adj_temp", m.AdjTemp, HasDefault: true),
            new("sType", m.SType, HasDefault: true),
        ]);
        await using var conn = await db.OpenAsync(ct);
        await conn.ExecuteAsync(new CommandDefinition(sql, p, cancellationToken: ct));
    }

    public async Task UpdateAsync(string machineIp, int probeNo, IReadOnlyDictionary<string, object?> columns, CancellationToken ct = default)
    {
        if (columns.Count == 0)
            return;

        var p = new DynamicParameters();
        var sets = new List<string>();
        var i = 0;
        foreach (var (column, value) in columns)
        {
            // Column names come from MachineUpdate's whitelist, never from raw input.
            sets.Add($"`{column}` = @v{i}");
            p.Add($"v{i}", value);
            i++;
        }
        p.Add("machineIp", machineIp);
        p.Add("probeNo", probeNo);

        await using var conn = await db.OpenAsync(ct);
        await conn.ExecuteAsync(new CommandDefinition(
            $"UPDATE master_machine SET {string.Join(", ", sets)} WHERE machine_ip = @machineIp AND probe_no = @probeNo",
            p, cancellationToken: ct));
    }

    public async Task DeleteAsync(string machineIp, int? probeNo, CancellationToken ct = default)
    {
        await using var conn = await db.OpenAsync(ct);
        var sql = probeNo is null
            ? "DELETE FROM master_machine WHERE machine_ip = @machineIp"
            : "DELETE FROM master_machine WHERE machine_ip = @machineIp AND probe_no = @probeNo";
        await conn.ExecuteAsync(new CommandDefinition(sql, new { machineIp, probeNo }, cancellationToken: ct));
    }

    public Task UpdateColorAsync(string machineIp, int probeNo, string color, CancellationToken ct = default) =>
        UpdateAsync(machineIp, probeNo, new Dictionary<string, object?> { ["color"] = color }, ct);

    public Task UpdateChkMonAsync(string machineIp, int probeNo, string value, CancellationToken ct = default) =>
        UpdateAsync(machineIp, probeNo, new Dictionary<string, object?> { ["chkMon"] = value }, ct);

    public async Task<List<LatestReading>> GetLatestReadingsAsync(CancellationToken ct = default)
    {
        // One query for every probe's latest reading instead of N+1.
        const string sql = """
            SELECT t1.machine_ip, t1.probe_no, t1.temp_value, t1.insert_time
            FROM temp_log t1
            INNER JOIN (
                SELECT machine_ip, probe_no, MAX(insert_time) AS max_time
                FROM temp_log
                GROUP BY machine_ip, probe_no
            ) t2 ON t1.machine_ip = t2.machine_ip
                 AND t1.probe_no = t2.probe_no
                 AND t1.insert_time = t2.max_time
            """;
        await using var conn = await db.OpenAsync(ct);
        var rows = await conn.QueryAsync<TempLogRow>(new CommandDefinition(sql, cancellationToken: ct));
        return rows.Select(r => new LatestReading(r.MachineIP, r.ProbeNo, TempLogRow.ToDouble(r.TempValue), r.InsertTime)).ToList();
    }
}
