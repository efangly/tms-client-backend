using Dapper;
using MySqlConnector;
using TmsBackend.Config;

namespace TmsBackend.Data;

/// <summary>Database liveness check, split out so background services can be tested without a server.</summary>
public interface IDbPing
{
    Task<bool> PingAsync(CancellationToken ct = default);
}

/// <summary>Pooled MySQL/MariaDB connections (Go: database.DB).</summary>
public sealed class Db : IAsyncDisposable, IDbPing
{
    static Db()
    {
        // machine_ip -> MachineIP, insert_time -> InsertTime, chkOnline -> ChkOnline, ...
        DefaultTypeMap.MatchNamesWithUnderscores = true;
    }

    private readonly MySqlDataSource _dataSource;

    public Db(AppOptions options)
    {
        _dataSource = new MySqlDataSource(BuildConnectionString(options));
    }

    /// <summary>
    /// Connection string equivalent to the Go DSN. No charset is sent:
    /// MySqlConnector always uses utf8mb4 and the server converts to each
    /// column's charset (tis620 included) — see spikes/README.md.
    /// </summary>
    public static string BuildConnectionString(AppOptions o) => new MySqlConnectionStringBuilder
    {
        Server = o.DbHost,
        Port = (uint)o.DbPort,
        UserID = o.DbUser,
        Password = o.DbPassword,
        Database = o.DbName,
        // Pool settings from database.go
        MaximumPoolSize = 100,
        ConnectionLifeTime = 3600,
        // Drop idle connections before MySQL's wait_timeout closes them.
        ConnectionIdleTimeout = 300,
        ConnectionTimeout = 15,
        // Zero dates read as DateTime.MinValue (Go: zero time.Time) instead of throwing.
        AllowZeroDateTime = true,
        ConvertZeroDateTime = true,
    }.ConnectionString;

    public async Task<MySqlConnection> OpenAsync(CancellationToken ct = default) =>
        await _dataSource.OpenConnectionAsync(ct);

    /// <summary>Opens a connection and pings the server; throws on failure.</summary>
    public async Task ConnectAsync(CancellationToken ct = default)
    {
        await using var conn = await OpenAsync(ct);
        if (!await conn.PingAsync(ct))
            throw new InvalidOperationException("database ping failed");
    }

    /// <summary>True when the database answers a ping (used by /health and the polling loops).</summary>
    public async Task<bool> PingAsync(CancellationToken ct = default)
    {
        try
        {
            await using var conn = await OpenAsync(ct);
            return await conn.PingAsync(ct);
        }
        catch (Exception e) when (e is MySqlException or InvalidOperationException or TimeoutException)
        {
            return false;
        }
    }

    public static bool IsDuplicateKey(Exception e) =>
        e is MySqlException { ErrorCode: MySqlErrorCode.DuplicateKeyEntry };

    public ValueTask DisposeAsync() => _dataSource.DisposeAsync();
}
