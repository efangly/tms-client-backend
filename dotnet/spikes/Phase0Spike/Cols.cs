using Dapper;
using MySqlConnector;

// Schema dump used while writing the Phase 2 repositories: column types,
// nullability, defaults and indexes of the tables the backend touches.
static class ColsSpike
{
    public static async Task Run(string cs)
    {
        await using var c = new MySqlConnection(cs);
        foreach (var r in await c.QueryAsync(@"SELECT TABLE_NAME t, COLUMN_NAME c, COLUMN_TYPE ty, IS_NULLABLE n, COLUMN_DEFAULT d, COLUMN_KEY k, EXTRA e
            FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE()
            AND TABLE_NAME IN ('master_machine','temp_log','temp_error','archive_manifest','temp_log_archive')
            ORDER BY TABLE_NAME, ORDINAL_POSITION"))
            Console.WriteLine($"{r.t}.{r.c} {r.ty} null={r.n} default={r.d ?? "<none>"} key={r.k} {r.e}");
        foreach (var r in await c.QueryAsync(@"SELECT TABLE_NAME t, INDEX_NAME i, GROUP_CONCAT(COLUMN_NAME ORDER BY SEQ_IN_INDEX) cols, NON_UNIQUE u
            FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = DATABASE()
            AND TABLE_NAME IN ('master_machine','temp_log','temp_error','archive_manifest','temp_log_archive')
            GROUP BY TABLE_NAME, INDEX_NAME, NON_UNIQUE"))
            Console.WriteLine($"INDEX {r.t}.{r.i} ({r.cols}) nonunique={r.u}");
        foreach (var r in await c.QueryAsync(@"SELECT TABLE_NAME t, TABLE_ROWS n FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE()
            AND TABLE_NAME IN ('master_machine','temp_log','temp_error','archive_manifest','temp_log_archive')"))
            Console.WriteLine($"ROWS~ {r.t} = {r.n}");
    }
}
