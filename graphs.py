import sqlite3
import matplotlib.pyplot as plt
import matplotlib.dates as mdates
import os
import pprint

# Connect to database
conn = sqlite3.connect('./db/checkpoint_sizes.db')
c = conn.cursor()

# Get data from database
c.execute('SELECT timestamp, containers, size FROM checkpoint_sizes')
data = c.fetchall()

# Create dictionary to store data by numContainers
container_data = {}
for container in set([x[1] for x in data]):
    container_data[container] = []

# Populate dictionary
for row in data:
    container_data[row[1]].append((row[0], row[2]))

pprint.pprint(container_data)

# Create figure
fig = plt.figure()

# Plot data for each numContainers
for container in container_data:
    x = [row[0] for row in container_data[container]]
    y = [row[1] for row in container_data[container]]

    # Plot the sizes as a bar chart
    plt.bar(x, y, color='b', align='center', width=0.2, label=f'{row[1]} container')
  
plt.xlabel('Number of containers')
plt.ylabel('Checkpoint size (MB)')
plt.title('Checkpoint sizes by number of containers')
plt.legend()
fig.autofmt_xdate()

# Save graph image to "fig" folder
if not os.path.exists("fig"):
    os.makedirs("fig")
fig.savefig("fig/checkpoint_sizes.png")

# Close database connection
conn.close()