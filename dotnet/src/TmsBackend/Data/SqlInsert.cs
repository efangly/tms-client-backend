using Dapper;

namespace TmsBackend.Data;

/// <summary>One column of an INSERT. <paramref name="HasDefault"/> mirrors a gorm:"default:..." tag.</summary>
public readonly record struct InsertColumn(string Name, object? Value, bool HasDefault = false);

public static class SqlInsert
{
    /// <summary>
    /// Builds an INSERT the way GORM's Create does: a column that has a DB
    /// default and a zero value ("" / 0 / null) is left out so the database
    /// default applies (e.g. master_machine.color → '000000').
    /// </summary>
    public static (string Sql, DynamicParameters Params) Build(string table, IEnumerable<InsertColumn> columns)
    {
        var included = columns.Where(c => !c.HasDefault || !IsZero(c.Value)).ToList();
        var p = new DynamicParameters();
        for (var i = 0; i < included.Count; i++)
            p.Add($"p{i}", included[i].Value);

        var names = string.Join(", ", included.Select(c => $"`{c.Name}`"));
        var values = string.Join(", ", included.Select((_, i) => $"@p{i}"));
        return ($"INSERT INTO `{table}` ({names}) VALUES ({values})", p);
    }

    internal static bool IsZero(object? value) => value switch
    {
        null => true,
        string s => s.Length == 0,
        int i => i == 0,
        long l => l == 0,
        double d => d == 0,
        _ => false,
    };
}
