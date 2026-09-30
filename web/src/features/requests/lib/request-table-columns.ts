export type RequestTableColumnWidths = readonly number[]

export const REQUEST_TABLE_COLUMN_DEFAULT_WIDTHS: RequestTableColumnWidths = [3, 10, 17, 10, 7, 14, 9, 8, 7, 7, 8]
export const REQUEST_TABLE_COLUMN_MINIMUM_WIDTHS: RequestTableColumnWidths = [3, 7, 10, 7, 6, 0, 7, 6, 6, 7, 7]
export const REQUEST_TABLE_COLUMN_SIZING = {
  storageKey: 'xlyra:requests:table-column-widths:v1',
  defaultWidths: REQUEST_TABLE_COLUMN_DEFAULT_WIDTHS,
  minimumWidths: REQUEST_TABLE_COLUMN_MINIMUM_WIDTHS,
}
