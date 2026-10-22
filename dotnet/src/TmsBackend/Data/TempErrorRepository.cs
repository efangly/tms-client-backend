using Dapper;
using TmsBackend.Models;

namespace TmsBackend.Data;

public interface ITempErrorRepository
{
    /// <summary>Throws a duplicate-key MySqlException (see <see cref="Db.IsDuplicateKey"/>) when the row exists.</summary>
    Task InsertAsync(TempError error, CancellationToken ct = default);
    /// <summary>Newest first. Dates (YYYY-MM-DD) apply only when both are set; limit follows GORM (negative = none).</summary>
    Task<List<TempError>> QueryAsync(string? startDate, string? endDate, int limit, CancellationToken ct = default);
}

public sealed class TempErrorRepository(Db db) : ITempErrorRepository
{
    public async Task InsertAsync(TempError e, CancellationToken ct = default)
    {
        // HasDefault follows the gorm:"default:..." tags in models.go.
        var (sql, p) = SqlInsert.Build("temp_error",
        [
            new("machine_ip", e.MachineIP),
            new("probe_no", e.ProbeNo),
            new("machine_name", e.MachineName),
            new("temp_value", e.TempValue),
            new("error_time", e.ErrorTime),
            new("sms_status", e.SmsStatus, HasDefault: true),
            new("sms_send_time", e.SmsSendTime),
            new("sms_send_status", e.SmsSendStatus, HasDefault: true),
            new("mail_status", e.MailStatus, HasDefault: true),
            new("mail_send_time", e.MailSendTime),
            new("mail_send_status", e.MailSendStatus, HasDefault: true),
            new("line_status", e.LineStatus, HasDefault: true),
            new("line_send_time", e.LineSendTime),
            new("line_send_status", e.LineSendStatus, HasDefault: true),
            new("min_temp", e.MinTemp),
            new("max_temp", e.MaxTemp),
            new("mon_status", e.MonStatus, HasDefault: true),
            new("mail_count", e.MailCount, HasDefault: true),
            new("sms_count", e.SmsCount, HasDefault: true),
            new("line_count", e.LineCount, HasDefault: true),
            new("sType", e.SType, HasDefault: true),
            new("temp_status", e.TempStatus, HasDefault: true),
            new("error_type", e.ErrorType, HasDefault: true),
        ]);
        await using var conn = await db.OpenAsync(ct);
        await conn.ExecuteAsync(new CommandDefinition(sql, p, cancellationToken: ct));
    }

    public async Task<List<TempError>> QueryAsync(string? startDate, string? endDate, int limit, CancellationToken ct = default)
    {
        var p = new DynamicParameters();
        var sql = "SELECT * FROM temp_error";
        if (!string.IsNullOrEmpty(startDate) && !string.IsNullOrEmpty(endDate))
        {
            sql += " WHERE error_time BETWEEN @start AND @end";
            p.Add("start", startDate + " 00:00:00");
            p.Add("end", endDate + " 23:59:59");
        }
        sql += " ORDER BY error_time DESC";
        if (limit >= 0)
        {
            sql += " LIMIT @limit";
            p.Add("limit", limit);
        }
        await using var conn = await db.OpenAsync(ct);
        return (await conn.QueryAsync<TempError>(new CommandDefinition(sql, p, cancellationToken: ct))).AsList();
    }
}
