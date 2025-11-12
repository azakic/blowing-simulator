import csv
import os
import re
import sys
from datetime import datetime

try:
    import camelot
except ImportError:
    print(
        "Error: camelot library is required. Install with 'pip install camelot-py[cv]'"
    )
    sys.exit(1)


def parse_date(date_str):
    try:
        return datetime.strptime(date_str, "%d.%m.%Y")
    except Exception:
        return None


def extract_last_lange(pdf_path):
    try:
        tables = camelot.read_pdf(pdf_path, pages="all")
        lange_values = []
        for table in tables:
            for idx, cell in enumerate(table.df.iloc[:, 0]):
                if idx == 0 and not cell.replace(".", "", 1).isdigit():
                    continue
                first_value = cell.split("\n")[0].replace(",", ".").strip()
                if first_value.replace(".", "", 1).isdigit():
                    lange_values.append(float(first_value))
        if lange_values:
            return lange_values[-1]
    except Exception as e:
        print(f"Error processing {pdf_path}: {e}")
    return ""


def extract_date_address(filename):
    match = re.match(r"(\d{2}\.\d{2}\.\d{4}),\s*[\d\s]+,\s*(.+)\.pdf$", filename)
    if match:
        date = match.group(1)
        address = match.group(2)
        return date, address
    else:
        parts = filename.split(os.sep)
        for part in reversed(parts):
            m = re.match(r"(\d{2}\.\d{2}\.\d{4}),\s*[\d\s]+,\s*(.+)", part)
            if m:
                return m.group(1), m.group(2)
        return "", ""


def main():
    args = sys.argv[1:]
    search_dir = "."
    start_date = None
    end_date = None

    if len(args) >= 1:
        search_dir = args[0]
    if len(args) == 2:
        start_date = parse_date(args[1])
        end_date = start_date
        if not start_date:
            print("Invalid date format. Use DD.MM.YYYY")
            sys.exit(1)
    elif len(args) == 3:
        start_date = parse_date(args[1])
        end_date = parse_date(args[2])
        if not start_date or not end_date:
            print("Invalid date format. Use DD.MM.YYYY")
            sys.exit(1)

    results = []
    for root, dirs, files in os.walk(search_dir):
        for filename in files:
            if filename.lower().endswith(".pdf"):
                date, address = extract_date_address(filename)
                file_date = parse_date(date)
                if start_date and end_date:
                    if not (file_date and start_date <= file_date <= end_date):
                        continue
                full_path = os.path.join(root, filename)
                last_lange = extract_last_lange(full_path)
                results.append((date, address, last_lange))

    # Sort results by date then address
    results_sorted = sorted(results, key=lambda item: (item[0], item[1]))

    # Dynamic CSV filename
    if start_date and end_date:
        if start_date == end_date:
            csv_filename = f"lange_camelot_{start_date.strftime('%d-%m-%Y')}.csv"
        else:
            csv_filename = f"lange_camelot_{start_date.strftime('%d-%m-%Y')}_to_{end_date.strftime('%d-%m-%Y')}.csv"
    else:
        csv_filename = "lange_camelot_all.csv"

    # Write results to CSV buffer instead of file
    import io

    csv_buffer = io.StringIO()
    writer = csv.writer(csv_buffer, delimiter=";")
    unique_addresses = set()
    total = 0
    writer.writerow(["date", "address", "last_lange"])
    for date, address, last_lange in results_sorted:
        writer.writerow([date, address, last_lange])
        unique_addresses.add(address)
        if isinstance(last_lange, float) or (
            isinstance(last_lange, str)
            and str(last_lange).replace(".", "", 1).isdigit()
        ):
            total += float(last_lange)
    writer.writerow(["SUM", "", total])
    writer.writerow(["UNIQUE_ADDRESSES", "", len(unique_addresses)])

    # Print CSV data to stdout with markers
    print("CSV_START")
    print(csv_buffer.getvalue().strip())
    print("CSV_END")

    print("Results processed in buffer")
    print("Summary:")
    print(f"  Total distance: {total}")
    print(f"  Unique addresses: {len(unique_addresses)}")
    print(f"  Entries: {len(results_sorted)}")


if __name__ == "__main__":
    main()
